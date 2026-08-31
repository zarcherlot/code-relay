package relay

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	newMeta    = ".code-relay"
	maxRunbook = 1 << 20
	maxQueue   = 50 << 20
)

var runbookID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var commitSHA = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
var receiptSHA = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
var branchRef = regexp.MustCompile(`^refs/heads/[A-Za-z0-9._/-]+$`)
var atomicPathLocks [64]sync.Mutex

type Runbook struct {
	ID           string
	SourceCommit string
	Target       string
	Objective    string
	Plan         []string
	Expected     []string
	Raw          string
}

type Check struct {
	Name     string  `json:"name"`
	Expected string  `json:"expected"`
	Actual   string  `json:"actual"`
	Status   string  `json:"status"`
	Duration float64 `json:"duration_seconds,omitempty"`
}
type Receipt struct {
	RunbookID     string            `json:"runbook_id"`
	SourceCommit  string            `json:"source_commit"`
	Status        string            `json:"status"`
	Checks        []Check           `json:"checks"`
	Risks         []string          `json:"risks"`
	NextActions   []string          `json:"next_actions"`
	VerifiedAt    string            `json:"verified_at"`
	Environment   map[string]string `json:"environment"`
	RunbookSHA256 string            `json:"runbook_sha256,omitempty"`
}

func meta(root string) string {
	return filepath.Join(root, newMeta)
}
func now() string { return time.Now().UTC().Truncate(time.Second).Format(time.RFC3339) }
func atomicJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomicFile(path, data)
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		if runtime.GOOS != "windows" {
			return err
		}
	}
	return nil
}
func readJSON(path string, value any) error {
	if err := rejectSymlinkComponents(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Size() > maxRunbook {
		return errors.New("refusing unsafe JSON file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func parseRunbook(raw string) (Runbook, error) {
	if len([]byte(raw)) > maxRunbook {
		return Runbook{}, errors.New("runbook.md 超过 1 MiB 大小限制")
	}
	t := Runbook{Raw: raw}
	meta := map[string]string{}
	section := ""
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), maxRunbook)
	for scanner.Scan() {
		line := scanner.Text()
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "## ") {
			section = strings.TrimSpace(strings.TrimPrefix(trim, "## "))
			continue
		}
		if strings.HasPrefix(trim, "-") {
			item := strings.TrimSpace(strings.TrimPrefix(trim, "-"))
			if i := strings.Index(item, ":"); i > 0 && section == "" {
				key, value := strings.TrimSpace(item[:i]), strings.TrimSpace(item[i+1:])
				if _, exists := meta[key]; exists {
					return Runbook{}, fmt.Errorf("runbook.md 元数据重复: %s", key)
				}
				meta[key] = value
			}
			if section == "Validation Plan" {
				item = strings.TrimSpace(strings.TrimLeft(item, "0123456789. "))
				if item != "" {
					t.Plan = append(t.Plan, item)
				}
			}
			if section == "Expected Results" && item != "" {
				t.Expected = append(t.Expected, item)
			}
		}
		if section == "Validation Plan" && len(trim) > 0 && trim[0] >= '0' && trim[0] <= '9' {
			item := trim[strings.IndexAny(trim, ".) ")+1:]
			if strings.TrimSpace(item) != "" {
				t.Plan = append(t.Plan, strings.TrimSpace(item))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Runbook{}, fmt.Errorf("无法读取 runbook.md: %w", err)
	}
	t.ID, t.SourceCommit, t.Target, t.Objective = meta["runbook_id"], meta["source_commit"], meta["target"], meta["objective"]
	if !runbookID.MatchString(t.ID) {
		return t, fmt.Errorf("非法 runbook_id: %s", t.ID)
	}
	if !commitSHA.MatchString(t.SourceCommit) {
		return t, errors.New("source_commit 必须是 7-64 位十六进制 SHA")
	}
	if t.Target == "" || len(t.Target) > maxFieldLength || t.Objective == "" || len(t.Objective) > maxFieldLength || len(t.Plan) == 0 || len(t.Plan) > 100 || len(t.Expected) == 0 || len(t.Expected) > 100 {
		return t, errors.New("runbook.md 缺少 target/objective/Validation Plan/Expected Results")
	}
	for _, item := range append(append([]string{}, t.Plan...), t.Expected...) {
		if len(item) == 0 || len(item) > maxFieldLength {
			return t, errors.New("Validation Plan 或 Expected Results 项非法或过长")
		}
	}
	return t, nil
}

func ValidateRunbookFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = parseRunbook(string(raw))
	return err
}

func parseCommand(command string) ([]string, error) {
	if len(command) > maxCommandLength {
		return nil, errors.New("命令超过 4096 字符限制")
	}
	for _, op := range shellOperators {
		if strings.Contains(command, op) {
			return nil, errors.New("不允许使用 shell 管道、重定向或串联操作符")
		}
	}
	var out []string
	var cur strings.Builder
	quoted := rune(0)
	escaped := false
	for _, r := range command {
		if escaped {
			cur.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quoted != '\'' {
			escaped = true
			continue
		}
		if quoted != 0 {
			if r == quoted {
				quoted = 0
			} else {
				cur.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quoted = r
			continue
		}
		if r == ' ' || r == '\t' {
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}
	if quoted != 0 {
		return nil, errors.New("命令引号不匹配")
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	if len(out) == 0 {
		return nil, errors.New("命令为空")
	}
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(out[0], ".exe"), ".cmd"), ".bat"))
	if strings.ContainsAny(name, `/\\`) || !allowedCommands[name] {
		return nil, fmt.Errorf("不允许的可执行文件: %s", out[0])
	}
	if err := validateCommandArguments(name, out[1:]); err != nil {
		return nil, err
	}
	lower := strings.ToLower(strings.Join(out, " "))
	for _, denied := range deniedTokens {
		if strings.Contains(lower, denied) {
			return nil, errors.New("命令被安全策略拦截")
		}
	}
	return out, nil
}

func runCommand(argv []string, cwd string, timeout int) (int, string, bool, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cwd
	configureProcess(cmd)
	cmd.Env = filteredEnvironment(sensitiveEnvKeys)
	output := newTailBuffer(maxOutputLength)
	cmd.Stdout, cmd.Stderr = output, output
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	var err error
	timedOut := false
	select {
	case err = <-done:
	case <-time.After(time.Duration(timeout) * time.Second):
		timedOut = true
		killProcessTree(cmd)
		select {
		case err = <-done:
		case <-time.After(5 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			err = <-done
		}
	}
	text := output.String()
	if timedOut {
		return -1, text, true, nil
	}
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), text, false, nil
		}
		return -1, text, false, err
	}
	return 0, text, false, nil
}

func RunRunbook(root, id string, timeout int, worktree string) (Receipt, error) {
	if err := validateRunbookID(id); err != nil {
		return Receipt{}, err
	}
	path, err := projectPath(root, "runbooks", id, "runbook.md")
	if err != nil {
		return Receipt{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Receipt{}, err
	}
	runbook, err := parseRunbook(string(raw))
	if err != nil {
		return Receipt{RunbookID: id, Status: "blocked", Checks: []Check{{Name: "Runbook 协议", Expected: "runbook.md 通过协议校验", Actual: err.Error(), Status: "blocked"}}, Risks: []string{"runbook.md 不符合 Code Relay 协议"}, NextActions: []string{"修正 runbook.md 后重新发布"}, VerifiedAt: now(), Environment: map[string]string{"platform": runtime.GOOS + "/" + runtime.GOARCH}}, nil
	}
	cwd, err := pathWithinRoot(root, root)
	if err != nil {
		return Receipt{}, err
	}
	if worktree != "" {
		cwd, err = pathWithinRoot(root, worktree)
		if err != nil {
			return blocked(runbook, err.Error()), nil
		}
	}
	if info, statErr := os.Stat(cwd); statErr != nil || !info.IsDir() {
		return blocked(runbook, "验证工作目录不存在或不是目录: "+cwd), nil
	}
	lock, lockErr := acquireRunbookLock(root, runbook.ID, 10*time.Second)
	if lockErr != nil {
		return Receipt{}, lockErr
	}
	defer lock.release()
	r := Receipt{RunbookID: runbook.ID, SourceCommit: runbook.SourceCommit, Status: "passed", VerifiedAt: now(), Environment: map[string]string{"platform": runtime.GOOS + "/" + runtime.GOARCH, "go": runtime.Version(), "cwd": cwd}}
	for i, item := range runbook.Plan {
		argv, e := parseCommand(item)
		name := fmt.Sprintf("验证命令 %d: %s", i+1, item)
		if e != nil {
			r.Status = "blocked"
			r.Checks = append(r.Checks, Check{Name: name, Expected: "命令通过安全策略", Actual: e.Error(), Status: "blocked"})
			r.Risks = append(r.Risks, "命令被拦截: "+item)
			continue
		}
		started := time.Now()
		code, out, to, e := runCommand(argv, cwd, max(1, min(timeout, maxTimeout)))
		if e != nil {
			r.Status = "failed"
			r.Checks = append(r.Checks, Check{Name: name, Expected: "命令可以启动并完成", Actual: e.Error(), Status: "failed"})
			continue
		}
		if to {
			r.Status = "failed"
			r.Checks = append(r.Checks, Check{Name: name, Expected: fmt.Sprintf("在 %ds 内完成", timeout), Actual: "命令超时; " + out, Status: "failed", Duration: time.Since(started).Seconds()})
			r.NextActions = append(r.NextActions, "检查超时命令: "+item)
			continue
		}
		status := "passed"
		if code != 0 {
			status = "failed"
			r.Status = "failed"
			r.NextActions = append(r.NextActions, "修复并重试: "+item)
		}
		r.Checks = append(r.Checks, Check{Name: name, Expected: "退出码为 0", Actual: fmt.Sprintf("退出码 %d; %s", code, strings.TrimSpace(out)), Status: status, Duration: time.Since(started).Seconds()})
	}
	if r.Status == "failed" && len(r.NextActions) == 0 {
		r.NextActions = []string{"查看验证日志并决定是否启动下一轮"}
	}
	r.RunbookSHA256 = sha256Hex([]byte(raw))
	return r, nil
}

// PersistReceipt writes both machine-readable and human-readable artifacts.
// The write is idempotent and keeps the stable directory contract.
func PersistReceipt(root string, receipt Receipt) error {
	if err := validateRunbookID(receipt.RunbookID); err != nil {
		return err
	}
	dir, err := projectPath(root, "receipts", receipt.RunbookID)
	if err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(dir, "receipt.json"), receipt); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Receipt\n\n- runbook_id: %s\n- source_commit: %s\n- status: %s\n- verified_at: %s\n\n## Checks\n", receipt.RunbookID, receipt.SourceCommit, receipt.Status, receipt.VerifiedAt)
	for _, check := range receipt.Checks {
		fmt.Fprintf(&b, "- %s: %s (expected: %s; actual: %s)\n", check.Name, check.Status, check.Expected, check.Actual)
	}
	return atomicText(filepath.Join(dir, "receipt.md"), b.String())
}

func atomicText(path, value string) error {
	return atomicJSONText(path, []byte(value))
}

func atomicJSONText(path string, data []byte) error {
	return writeAtomicFile(path, data)
}

func writeAtomicFile(path string, data []byte) error {
	pathHash := sha256.Sum256([]byte(filepath.Clean(path)))
	mutex := &atomicPathLocks[int(pathHash[0])%len(atomicPathLocks)]
	mutex.Lock()
	defer mutex.Unlock()
	if err := rejectSymlinkComponents(path); err != nil {
		return err
	}
	if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	if err := rejectSymlinkComponents(filepath.Dir(path)); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}
func blocked(t Runbook, actual string) Receipt {
	return Receipt{RunbookID: t.ID, SourceCommit: t.SourceCommit, Status: "blocked", Checks: []Check{{Name: "验证工作目录", Expected: "位于仓库目录内且存在", Actual: actual, Status: "blocked"}}, Risks: []string{"验证工作目录必须位于绑定工程内"}, NextActions: []string{"选择工程内的隔离 worktree 后重新执行"}, VerifiedAt: now(), Environment: map[string]string{"platform": runtime.GOOS + "/" + runtime.GOARCH}}
}
func sha256Hex(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func Status(root string) (any, error) {
	base, err := projectPath(root, "runbooks")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return []any{}, nil
	}
	if err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t, runbookErr := readRunbook(root, e.Name())
		if runbookErr != nil {
			if runbookID.MatchString(e.Name()) {
				rows = append(rows, map[string]any{"runbook_id": e.Name(), "status": "invalid", "error": runbookErr.Error()})
			}
			continue
		}
		rp, pathErr := projectPath(root, "receipts", t.ID, "receipt.json")
		if pathErr != nil {
			return nil, pathErr
		}
		status := "runbook_published"
		var rec Receipt
		if readJSON(rp, &rec) == nil && validateReceipt(rec, t) == nil {
			status = rec.Status
		} else if _, statErr := os.Stat(rp); statErr == nil {
			status = "invalid"
		}
		rows = append(rows, map[string]any{"runbook_id": t.ID, "source_commit": t.SourceCommit, "target": t.Target, "status": status})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["runbook_id"].(string) < rows[j]["runbook_id"].(string) })
	return rows, nil
}

func validateReceipt(receipt Receipt, runbook Runbook) error {
	if receipt.RunbookID != runbook.ID || receipt.SourceCommit != runbook.SourceCommit {
		return errors.New("receipt runbook binding mismatch")
	}
	if receipt.Status != "passed" && receipt.Status != "failed" && receipt.Status != "blocked" {
		return errors.New("invalid receipt status")
	}
	if len(receipt.Checks) > 100 {
		return errors.New("too many receipt checks")
	}
	if len(receipt.Risks) > 100 || len(receipt.NextActions) > 100 {
		return errors.New("too many receipt risks or next actions")
	}
	if receipt.RunbookSHA256 != "" && !receiptSHA.MatchString(receipt.RunbookSHA256) {
		return errors.New("invalid receipt runbook_sha256")
	}
	if len(receipt.VerifiedAt) > maxFieldLength {
		return errors.New("receipt verified_at too long")
	}
	for _, item := range append(append([]string{}, receipt.Risks...), receipt.NextActions...) {
		if len(item) > maxFieldLength {
			return errors.New("receipt risk or next action too long")
		}
	}
	for _, check := range receipt.Checks {
		if check.Status != "passed" && check.Status != "failed" && check.Status != "blocked" {
			return errors.New("invalid receipt check status")
		}
		if len(check.Name) > maxFieldLength || len(check.Expected) > maxFieldLength || len(check.Actual) > maxFieldLength {
			return errors.New("receipt field too long")
		}
	}
	return nil
}

func Doctor(root string) (map[string]any, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	checks := []map[string]string{}
	add := func(name, status, detail string) {
		checks = append(checks, map[string]string{"name": name, "status": status, "detail": detail})
	}
	if info, statErr := os.Stat(rootAbs); statErr != nil || !info.IsDir() {
		add("root", "error", "工程根目录不存在")
		return map[string]any{"status": "error", "root": rootAbs, "checks": checks}, nil
	}
	if symlinkErr := rejectSymlinkComponents(rootAbs); symlinkErr != nil {
		add("root", "error", symlinkErr.Error())
		return map[string]any{"status": "error", "root": rootAbs, "checks": checks}, nil
	}
	add("root", "ok", rootAbs)
	if out, gitErr := runGit(rootAbs, gitTimeout, "rev-parse", "--show-prefix"); gitErr == nil && strings.TrimSpace(string(out)) == "" {
		add("git", "ok", "当前目录是 Git 工程根目录")
	} else {
		add("git", "error", "无法识别 Git 工程根目录")
	}
	metadata := filepath.Join(rootAbs, newMeta)
	if info, statErr := os.Stat(metadata); statErr == nil && info.IsDir() {
		add("metadata", "ok", metadata)
		if file, openErr := os.OpenFile(filepath.Join(metadata, ".doctor-write-test"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600); openErr == nil {
			_ = file.Close()
			_ = os.Remove(filepath.Join(metadata, ".doctor-write-test"))
			add("metadata-write", "ok", "元数据目录可写")
		} else {
			add("metadata-write", "error", openErr.Error())
		}
	} else {
		add("metadata", "warning", "尚未初始化 .code-relay/")
	}
	if _, projectErr := os.Stat(filepath.Join(metadata, "project.json")); projectErr == nil {
		add("binding", "ok", "发现 orchestrator 绑定")
	} else if _, checkpointErr := os.Stat(filepath.Join(metadata, "checkpoint.json")); checkpointErr == nil {
		add("binding", "ok", "发现 checkpoint 绑定")
	} else {
		add("binding", "warning", "尚未发现绑定配置")
	}
	if out, remoteErr := runGit(rootAbs, gitTimeout, "config", "--get", "remote.origin.url"); remoteErr == nil && strings.TrimSpace(string(out)) != "" {
		if remote, sanitizeErr := sanitizeRemote(string(out)); sanitizeErr == nil {
			add("remote", "ok", remote)
		} else {
			add("remote", "error", sanitizeErr.Error())
		}
	} else {
		add("remote", "warning", "未配置 origin remote")
	}
	status := "ok"
	for _, check := range checks {
		if check["status"] == "error" {
			status = "error"
			break
		}
		if check["status"] == "warning" {
			status = "warning"
		}
	}
	return map[string]any{"status": status, "root": rootAbs, "checks": checks}, nil
}

func Watch(root string, interval float64) error {
	if interval < 1 || interval > 3600 {
		return errors.New("poll interval must be 1..3600")
	}
	for {
		if err := syncRunbooks(root); err != nil {
			fmt.Fprintln(os.Stderr, err)
			logEvent("sync_error", map[string]any{"runtime": "go"})
		}
		time.Sleep(time.Duration(interval * float64(time.Second)))
	}
}
func syncRunbooks(root string) error {
	root, err := pathWithinRoot(root, root)
	if err != nil {
		return err
	}
	cfg := map[string]any{}
	configPath, err := projectPath(root, newMeta, "checkpoint.json")
	if err != nil {
		return err
	}
	if err := readJSON(configPath, &cfg); err != nil {
		return err
	}
	if version, ok := cfg["schema_version"].(float64); !ok || version != bindingSchemaVersion {
		return errors.New("invalid checkpoint schema_version")
	}
	repository, ok := cfg["repository"].(string)
	if !ok {
		return errors.New("invalid checkpoint repository")
	}
	repository, err = sanitizeRemote(repository)
	if err != nil {
		return errors.New("invalid checkpoint repository")
	}
	remote, err := canonicalRepo(root)
	if err != nil || remote != repository {
		return errors.New("checkpoint repository does not match origin")
	}
	ref, _ := cfg["ref"].(string)
	if !branchRef.MatchString(ref) {
		return errors.New("invalid checkpoint ref")
	}
	if _, err := runGit(root, gitTimeout, "fetch", "--quiet", "origin", ref); err != nil {
		return err
	}
	out, err := runGit(root, gitTimeout, "ls-tree", "-r", "--name-only", "FETCH_HEAD", "--", "runbooks")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, "/")
		if len(parts) != 3 || parts[0] != "runbooks" || parts[2] != "runbook.md" || !runbookID.MatchString(parts[1]) {
			continue
		}
		shown, er := runGit(root, gitTimeout, "show", "FETCH_HEAD:"+line)
		if er != nil || len(shown) > maxRunbook {
			continue
		}
		dest, pathErr := projectPath(root, newMeta, "inbox", "runbooks", parts[1], "runbook.md")
		if pathErr != nil {
			return pathErr
		}
		if _, er = os.Stat(dest); er == nil {
			continue
		}
		if er = atomicJSONText(dest, shown); er != nil {
			return er
		}
	}
	return nil
}

func runGit(root string, timeoutSeconds int, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	configureProcess(cmd)
	cmd.Env = gitEnvironment()
	output := newTailBuffer(maxQueue)
	cmd.Stdout, cmd.Stderr = output, output
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(time.Duration(timeoutSeconds) * time.Second):
		killProcessTree(cmd)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
		}
		return nil, fmt.Errorf("git operation timed out after %ds: %s", timeoutSeconds, strings.Join(args, " "))
	}
	out := output.String()
	if output.truncated() {
		return nil, errors.New("git output exceeds 50 MiB safety limit")
	}
	if err != nil {
		if strings.TrimSpace(out) != "" {
			return nil, errors.New(redactSensitive(strings.TrimSpace(out)))
		}
		return nil, errors.New(redactSensitive(err.Error()))
	}
	return []byte(out), nil
}
