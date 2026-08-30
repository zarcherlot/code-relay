package relay

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	newMeta  = ".code-relay"
	maxTask  = 1 << 20
	maxQueue = 50 << 20
)

var taskID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var commitSHA = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
var branchRef = regexp.MustCompile(`^refs/heads/[A-Za-z0-9._/-]+$`)
var atomicPathLocks [64]sync.Mutex

type Task struct {
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
	TaskID       string            `json:"task_id"`
	SourceCommit string            `json:"source_commit"`
	Status       string            `json:"status"`
	Checks       []Check           `json:"checks"`
	Risks        []string          `json:"risks"`
	NextActions  []string          `json:"next_actions"`
	VerifiedAt   string            `json:"verified_at"`
	Environment  map[string]string `json:"environment"`
	TaskSHA256   string            `json:"task_sha256,omitempty"`
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
	if info.Mode()&os.ModeSymlink != 0 || info.Size() > maxTask {
		return errors.New("refusing unsafe JSON file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func parseTask(raw string) (Task, error) {
	if len([]byte(raw)) > maxTask {
		return Task{}, errors.New("task.md 超过 1 MiB 大小限制")
	}
	t := Task{Raw: raw}
	meta := map[string]string{}
	section := ""
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), maxTask)
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
					return Task{}, fmt.Errorf("task.md 元数据重复: %s", key)
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
		return Task{}, fmt.Errorf("无法读取 task.md: %w", err)
	}
	t.ID, t.SourceCommit, t.Target, t.Objective = meta["task_id"], meta["source_commit"], meta["target"], meta["objective"]
	if !taskID.MatchString(t.ID) {
		return t, fmt.Errorf("非法 task_id: %s", t.ID)
	}
	if !commitSHA.MatchString(t.SourceCommit) {
		return t, errors.New("source_commit 必须是 7-64 位十六进制 SHA")
	}
	if t.Target == "" || len(t.Target) > maxFieldLength || t.Objective == "" || len(t.Objective) > maxFieldLength || len(t.Plan) == 0 || len(t.Plan) > 100 || len(t.Expected) == 0 || len(t.Expected) > 100 {
		return t, errors.New("task.md 缺少 target/objective/Validation Plan/Expected Results")
	}
	for _, item := range append(append([]string{}, t.Plan...), t.Expected...) {
		if len(item) == 0 || len(item) > maxFieldLength {
			return t, errors.New("Validation Plan 或 Expected Results 项非法或过长")
		}
	}
	return t, nil
}

func ValidateTaskFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = parseTask(string(raw))
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
	env := os.Environ()
	filtered := env[:0]
	for _, e := range env {
		key := strings.ToUpper(strings.SplitN(e, "=", 2)[0])
		if sensitiveEnvKeys[key] {
			continue
		}
		filtered = append(filtered, e)
	}
	cmd.Env = filtered
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

func RunTask(root, id string, timeout int, worktree string) (Receipt, error) {
	if err := validateTaskID(id); err != nil {
		return Receipt{}, err
	}
	path, err := projectPath(root, "tasks", id, "task.md")
	if err != nil {
		return Receipt{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Receipt{}, err
	}
	task, err := parseTask(string(raw))
	if err != nil {
		return Receipt{TaskID: id, Status: "blocked", Checks: []Check{{Name: "任务协议", Expected: "task.md 通过协议校验", Actual: err.Error(), Status: "blocked"}}, Risks: []string{"task.md 不符合 Code Relay 协议"}, NextActions: []string{"修正 task.md 后重新发布"}, VerifiedAt: now(), Environment: map[string]string{"platform": runtime.GOOS + "/" + runtime.GOARCH}}, nil
	}
	cwd, err := pathWithinRoot(root, root)
	if err != nil {
		return Receipt{}, err
	}
	if worktree != "" {
		cwd, err = pathWithinRoot(root, worktree)
		if err != nil {
			return blocked(task, err.Error()), nil
		}
	}
	if info, statErr := os.Stat(cwd); statErr != nil || !info.IsDir() {
		return blocked(task, "验证工作目录不存在或不是目录: "+cwd), nil
	}
	lock, lockErr := acquireTaskLock(root, task.ID, 10*time.Second)
	if lockErr != nil {
		return Receipt{}, lockErr
	}
	defer lock.release()
	r := Receipt{TaskID: task.ID, SourceCommit: task.SourceCommit, Status: "passed", VerifiedAt: now(), Environment: map[string]string{"platform": runtime.GOOS + "/" + runtime.GOARCH, "go": runtime.Version(), "cwd": cwd}}
	for i, item := range task.Plan {
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
	r.TaskSHA256 = sha256Hex([]byte(raw))
	return r, nil
}

// PersistReceipt writes both machine-readable and human-readable artifacts.
// The write is idempotent and keeps the stable directory contract.
func PersistReceipt(root string, receipt Receipt) error {
	if err := validateTaskID(receipt.TaskID); err != nil {
		return err
	}
	dir, err := projectPath(root, "receipts", receipt.TaskID)
	if err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(dir, "receipt.json"), receipt); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Receipt\n\n- task_id: %s\n- source_commit: %s\n- status: %s\n- verified_at: %s\n\n## Checks\n", receipt.TaskID, receipt.SourceCommit, receipt.Status, receipt.VerifiedAt)
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
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
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
func blocked(t Task, actual string) Receipt {
	return Receipt{TaskID: t.ID, SourceCommit: t.SourceCommit, Status: "blocked", Checks: []Check{{Name: "验证工作目录", Expected: "位于仓库目录内且存在", Actual: actual, Status: "blocked"}}, Risks: []string{"验证工作目录必须位于绑定工程内"}, NextActions: []string{"选择工程内的隔离 worktree 后重新执行"}, VerifiedAt: now(), Environment: map[string]string{"platform": runtime.GOOS + "/" + runtime.GOARCH}}
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
	base, err := projectPath(root, "tasks")
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
		t, taskErr := readTask(root, e.Name())
		if taskErr != nil {
			if taskID.MatchString(e.Name()) {
				rows = append(rows, map[string]any{"task_id": e.Name(), "status": "invalid", "error": taskErr.Error()})
			}
			continue
		}
		rp, pathErr := projectPath(root, "receipts", t.ID, "receipt.json")
		if pathErr != nil {
			return nil, pathErr
		}
		status := "task_published"
		var rec Receipt
		if readJSON(rp, &rec) == nil && validateReceipt(rec, t) == nil {
			status = rec.Status
		} else if _, statErr := os.Stat(rp); statErr == nil {
			status = "invalid"
		}
		rows = append(rows, map[string]any{"task_id": t.ID, "source_commit": t.SourceCommit, "target": t.Target, "status": status})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["task_id"].(string) < rows[j]["task_id"].(string) })
	return rows, nil
}

func validateReceipt(receipt Receipt, task Task) error {
	if receipt.TaskID != task.ID || receipt.SourceCommit != task.SourceCommit {
		return errors.New("receipt task binding mismatch")
	}
	if receipt.Status != "passed" && receipt.Status != "failed" && receipt.Status != "blocked" {
		return errors.New("invalid receipt status")
	}
	if len(receipt.Checks) > 100 {
		return errors.New("too many receipt checks")
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
	if out, gitErr := runGit(rootAbs, gitTimeout, "rev-parse", "--show-toplevel"); gitErr == nil && samePath(strings.TrimSpace(string(out)), rootAbs) {
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
	} else if _, verifierErr := os.Stat(filepath.Join(metadata, "verifier.json")); verifierErr == nil {
		add("binding", "ok", "发现 verifier 绑定")
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
		if err := syncTasks(root); err != nil {
			fmt.Fprintln(os.Stderr, err)
			logEvent("sync_error", map[string]any{"runtime": "go"})
		}
		time.Sleep(time.Duration(interval * float64(time.Second)))
	}
}
func syncTasks(root string) error {
	root, err := pathWithinRoot(root, root)
	if err != nil {
		return err
	}
	cfg := map[string]any{}
	configPath, err := projectPath(root, newMeta, "verifier.json")
	if err != nil {
		return err
	}
	if err := readJSON(configPath, &cfg); err != nil {
		return err
	}
	if version, ok := cfg["schema_version"].(float64); !ok || version != 1 {
		return errors.New("invalid verifier schema_version")
	}
	repository, ok := cfg["repository"].(string)
	if !ok {
		return errors.New("invalid verifier repository")
	}
	repository, err = sanitizeRemote(repository)
	if err != nil {
		return errors.New("invalid verifier repository")
	}
	remote, err := canonicalRepo(root)
	if err != nil || remote != repository {
		return errors.New("verifier repository does not match origin")
	}
	ref, _ := cfg["ref"].(string)
	if !branchRef.MatchString(ref) {
		return errors.New("invalid verifier ref")
	}
	if _, err := runGit(root, gitTimeout, "fetch", "--quiet", "origin", ref); err != nil {
		return err
	}
	out, err := runGit(root, gitTimeout, "ls-tree", "-r", "--name-only", "FETCH_HEAD", "--", "tasks")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, "/")
		if len(parts) != 3 || parts[0] != "tasks" || parts[2] != "task.md" || !taskID.MatchString(parts[1]) {
			continue
		}
		shown, er := runGit(root, gitTimeout, "show", "FETCH_HEAD:"+line)
		if er != nil || len(shown) > maxTask {
			continue
		}
		dest, pathErr := projectPath(root, newMeta, "inbox", "tasks", parts[1], "task.md")
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

func Daemon(root, role string, interval float64, addr string) error {
	if role != "orchestrator" && role != "verifier" {
		return errors.New("role must be orchestrator or verifier")
	}
	if interval < 1 || interval > 3600 {
		return errors.New("poll interval must be 1..3600")
	}
	var err error
	root, err = pathWithinRoot(root, root)
	if err != nil {
		return err
	}
	d := &daemon{root: root, role: role, requests: map[string][]time.Time{}}
	stopWatcher := make(chan struct{})
	if role == "verifier" {
		go func() {
			for {
				select {
				case <-stopWatcher:
					return
				default:
					if err := syncTasks(root); err != nil {
						fmt.Fprintln(os.Stderr, err)
					}
					time.Sleep(time.Duration(interval * float64(time.Second)))
				}
			}
		}()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", d.handle)
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() { <-ctx.Done(); close(stopWatcher); _ = server.Shutdown(context.Background()) }()
	fmt.Println("Code Relay daemon listening on", addr)
	logEvent("daemon_started", map[string]any{"role": role, "addr": addr})
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type daemon struct {
	root, role string
	mu         sync.Mutex
	requests   map[string][]time.Time
}

func (d *daemon) allowRequest(client string) bool {
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	recent := d.requests[client][:0]
	for _, stamp := range d.requests[client] {
		if now.Sub(stamp) < time.Minute {
			recent = append(recent, stamp)
		}
	}
	if len(recent) >= 60 {
		d.requests[client] = recent
		return false
	}
	d.requests[client] = append(recent, now)
	return true
}

func (d *daemon) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	client := r.RemoteAddr
	if host, _, splitErr := net.SplitHostPort(r.RemoteAddr); splitErr == nil {
		client = host
	}
	if !d.allowRequest(client) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	contentType := strings.ToLower(strings.Split(r.Header.Get("Content-Type"), ";")[0])
	if contentType != "application/json" {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	if r.ContentLength > 1<<20 {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil {
		w.WriteHeader(400)
		return
	}
	if len(body) > 1<<20 {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	secret := os.Getenv("CODE_RELAY_WEBHOOK_SECRET")
	if secret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		want := "sha256=" + fmt.Sprintf("%x", mac.Sum(nil))
		if !hmac.Equal([]byte(sig), []byte(want)) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	m, pathErr := projectPath(d.root, newMeta)
	if pathErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	_ = os.MkdirAll(m, 0700)
	delivery := r.Header.Get("X-GitHub-Delivery")
	d.mu.Lock()
	defer d.mu.Unlock()
	if delivery != "" {
		var deliveries []string
		_ = readJSON(filepath.Join(m, "deliveries.json"), &deliveries)
		for _, seen := range deliveries {
			if seen == delivery {
				w.WriteHeader(http.StatusAccepted)
				return
			}
		}
		deliveries = append(deliveries, delivery)
		if len(deliveries) > 10000 {
			deliveries = deliveries[len(deliveries)-10000:]
		}
		if err := atomicJSON(filepath.Join(m, "deliveries.json"), deliveries); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	p := filepath.Join(m, "events.jsonl")
	if info, e := os.Stat(p); e == nil && info.Size() >= maxQueue {
		w.WriteHeader(http.StatusInsufficientStorage)
		return
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		w.WriteHeader(500)
		return
	}
	defer f.Close()
	_, _ = f.Write(append(body, '\n'))
	w.WriteHeader(http.StatusAccepted)
}
