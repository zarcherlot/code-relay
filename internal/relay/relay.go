package relay

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	newMeta   = ".code-relay"
	oldMeta   = ".codex-relay"
	maxTask   = 1 << 20
	maxOutput = 4000
	maxQueue  = 50 << 20
)

var taskID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var commitSHA = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
var branchRef = regexp.MustCompile(`^refs/heads/[A-Za-z0-9._/-]+$`)

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
	newPath, oldPath := filepath.Join(root, newMeta), filepath.Join(root, oldMeta)
	if _, err := os.Stat(newPath); err == nil {
		return newPath
	}
	if _, err := os.Stat(oldPath); err == nil {
		return oldPath
	}
	return newPath
}
func bothMeta(root string) []string { return []string{meta(root), filepath.Join(root, oldMeta)} }
func now() string                   { return time.Now().UTC().Truncate(time.Second).Format(time.RFC3339) }
func atomicJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err = os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func readJSON(path string, value any) error {
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
				meta[strings.TrimSpace(item[:i])] = strings.TrimSpace(item[i+1:])
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
	t.ID, t.SourceCommit, t.Target, t.Objective = meta["task_id"], meta["source_commit"], meta["target"], meta["objective"]
	if !taskID.MatchString(t.ID) {
		return t, fmt.Errorf("非法 task_id: %s", t.ID)
	}
	if !commitSHA.MatchString(t.SourceCommit) {
		return t, errors.New("source_commit 必须是 7-64 位十六进制 SHA")
	}
	if t.Target == "" || t.Objective == "" || len(t.Plan) == 0 || len(t.Expected) == 0 {
		return t, errors.New("task.md 缺少 target/objective/Validation Plan/Expected Results")
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
	if len(command) > 4096 {
		return nil, errors.New("命令超过 4096 字符限制")
	}
	for _, op := range []string{"&&", "||", "|", ";", "&", ">", "<"} {
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
	allowed := map[string]bool{"python": true, "python3": true, "pytest": true, "py": true, "node": true, "npm": true, "go": true, "cargo": true, "dotnet": true, "bash": true, "sh": true, "pwsh": true, "powershell": true, "cmd": true, "echo": true}
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(out[0], ".exe"), ".cmd"), ".bat"))
	if strings.ContainsAny(name, `/\\`) || !allowed[name] {
		return nil, fmt.Errorf("不允许的可执行文件: %s", out[0])
	}
	lower := strings.ToLower(strings.Join(out, " "))
	for _, denied := range []string{"rm -rf", "rmdir /s", "del /s", "format ", "shutdown", "git push", "git reset --hard", "curl | sh", "wget | sh", "invoke-webrequest", "pip install", "npm install"} {
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
		key := strings.SplitN(e, "=", 2)[0]
		switch key {
		case "GITHUB_TOKEN", "GH_TOKEN", "CODE_RELAY_INVITE_SECRET", "CODEX_RELAY_INVITE_SECRET", "CODEX_API_KEY", "OPENAI_API_KEY":
			continue
		}
		filtered = append(filtered, e)
	}
	cmd.Env = filtered
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	var err error
	timedOut := false
	select {
	case err = <-done:
	case <-time.After(time.Duration(timeout) * time.Second):
		timedOut = true
		killProcessTree(cmd)
		err = <-done
	}
	text := output.Bytes()
	if len(text) > maxOutput {
		text = text[len(text)-maxOutput:]
	}
	if timedOut {
		return -1, string(text), true, nil
	}
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), string(text), false, nil
		}
		return -1, string(text), false, err
	}
	return 0, string(text), false, nil
}

func RunTask(root, id string, timeout int, worktree string) (Receipt, error) {
	path := filepath.Join(root, "tasks", id, "task.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return Receipt{}, err
	}
	task, err := parseTask(string(raw))
	if err != nil {
		return Receipt{TaskID: id, Status: "blocked", Checks: []Check{{Name: "任务协议", Expected: "task.md 通过协议校验", Actual: err.Error(), Status: "blocked"}}, Risks: []string{"task.md 不符合 Code Relay 协议"}, NextActions: []string{"修正 task.md 后重新发布"}, VerifiedAt: now(), Environment: map[string]string{"platform": runtime.GOOS + "/" + runtime.GOARCH}}, nil
	}
	cwd := root
	if worktree != "" {
		cwd, _ = filepath.Abs(worktree)
	}
	rootAbs, _ := filepath.Abs(root)
	cwdAbs, _ := filepath.Abs(cwd)
	if cwdAbs != rootAbs && !strings.HasPrefix(cwdAbs, rootAbs+string(os.PathSeparator)) {
		return blocked(task, "拒绝工作目录: "+cwdAbs), nil
	}
	if info, statErr := os.Stat(cwdAbs); statErr != nil || !info.IsDir() {
		return blocked(task, "验证工作目录不存在或不是目录: "+cwdAbs), nil
	}
	r := Receipt{TaskID: task.ID, SourceCommit: task.SourceCommit, Status: "passed", VerifiedAt: now(), Environment: map[string]string{"platform": runtime.GOOS + "/" + runtime.GOARCH, "go": runtime.Version(), "cwd": cwdAbs}}
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
		code, out, to, e := runCommand(argv, cwdAbs, max(1, min(timeout, 3600)))
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
// The write is idempotent and keeps the same directory contract as Python.
func PersistReceipt(root string, receipt Receipt) error {
	dir := filepath.Join(root, "receipts", receipt.TaskID)
	if err := atomicJSON(filepath.Join(dir, "receipt.json"), receipt); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Receipt\n\n- task_id: %s\n- source_commit: %s\n- status: %s\n- verified_at: %s\n\n## Checks\n", receipt.TaskID, receipt.SourceCommit, receipt.Status, receipt.VerifiedAt)
	for _, check := range receipt.Checks {
		fmt.Fprintf(&b, "- %s: %s (expected: %s; actual: %s)\n", check.Name, check.Status, check.Expected, check.Actual)
	}
	return os.WriteFile(filepath.Join(dir, "receipt.md"), []byte(b.String()), 0600)
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
	base := filepath.Join(root, "tasks")
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
		raw, er := os.ReadFile(filepath.Join(base, e.Name(), "task.md"))
		if er != nil {
			continue
		}
		t, er := parseTask(string(raw))
		if er != nil {
			rows = append(rows, map[string]any{"task_id": e.Name(), "status": "invalid", "error": er.Error()})
			continue
		}
		status := "task_published"
		rp := filepath.Join(root, "receipts", t.ID, "receipt.json")
		var rec Receipt
		if readJSON(rp, &rec) == nil {
			status = rec.Status
		}
		rows = append(rows, map[string]any{"task_id": t.ID, "source_commit": t.SourceCommit, "target": t.Target, "status": status})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["task_id"].(string) < rows[j]["task_id"].(string) })
	return rows, nil
}

func Watch(root string, interval float64) error {
	if interval < 1 || interval > 3600 {
		return errors.New("poll interval must be 1..3600")
	}
	for {
		if err := syncTasks(root); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		time.Sleep(time.Duration(interval * float64(time.Second)))
	}
}
func syncTasks(root string) error {
	cfg := map[string]any{}
	if err := readJSON(filepath.Join(meta(root), "verifier.json"), &cfg); err != nil {
		return err
	}
	ref, _ := cfg["ref"].(string)
	if !branchRef.MatchString(ref) {
		return errors.New("invalid verifier ref")
	}
	fetch := exec.Command("git", "fetch", "--quiet", "origin", ref)
	fetch.Dir = root
	if err := fetch.Run(); err != nil {
		return err
	}
	tree := exec.Command("git", "ls-tree", "-r", "--name-only", "FETCH_HEAD", "--", "tasks")
	tree.Dir = root
	out, err := tree.Output()
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, "/")
		if len(parts) != 3 || parts[0] != "tasks" || parts[2] != "task.md" || !taskID.MatchString(parts[1]) {
			continue
		}
		show := exec.Command("git", "show", "FETCH_HEAD:"+line)
		show.Dir = root
		shown, er := show.Output()
		if er != nil || len(shown) > maxTask {
			continue
		}
		dest := filepath.Join(meta(root), "inbox", line)
		if _, er = os.Stat(dest); er == nil {
			continue
		}
		if er = os.MkdirAll(filepath.Dir(dest), 0700); er == nil {
			er = os.WriteFile(dest, shown, 0600)
		}
		if er != nil {
			return er
		}
	}
	return nil
}

func Daemon(root, role string, interval float64, addr string) error {
	if role != "orchestrator" && role != "verifier" {
		return errors.New("role must be orchestrator or verifier")
	}
	d := &daemon{root: root, role: role}
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
	server := &http.Server{Addr: addr, Handler: mux}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() { <-ctx.Done(); close(stopWatcher); _ = server.Shutdown(context.Background()) }()
	fmt.Println("Code Relay daemon listening on", addr)
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type daemon struct{ root, role string }

func (d *daemon) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.ContentLength > 1<<20 {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(400)
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
	m := meta(d.root)
	_ = os.MkdirAll(m, 0700)
	delivery := r.Header.Get("X-GitHub-Delivery")
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
