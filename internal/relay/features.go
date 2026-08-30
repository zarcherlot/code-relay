package relay

// This file contains the user-facing orchestration operations.  They are
// deliberately implemented in the same Go binary as the verifier so the
// plugin has no language runtime dependency.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var inviteNonce = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

func canonicalRepo(root string) (string, error) {
	out, err := runGit(root, gitTimeout, "config", "--get", "remote.origin.url")
	if err != nil {
		return "", errors.New("无法读取 origin remote")
	}
	value := strings.TrimSpace(string(out))
	if strings.HasSuffix(value, ".git") {
		value = strings.TrimSuffix(value, ".git")
	}
	if strings.HasPrefix(value, "git@github.com:") {
		value = "https://github.com/" + strings.TrimPrefix(value, "git@github.com:")
	}
	if value == "" || len(value) > 2048 || strings.IndexFunc(value, func(r rune) bool { return r < 32 || r == ' ' || r == '\t' || r == '\n' }) >= 0 {
		return "", errors.New("origin URL 非法或过长")
	}
	for _, prefix := range []string{"ext::", "file://", "git+file://", "fd::"} {
		if strings.HasPrefix(strings.ToLower(value), prefix) {
			return "", errors.New("不允许使用本地协议或 ext transport")
		}
	}
	return value, nil
}

func currentRef(root string) (string, error) {
	out, err := runGit(root, gitTimeout, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", errors.New("无法读取当前分支")
	}
	ref := "refs/heads/" + strings.TrimSpace(string(out))
	if !branchRef.MatchString(ref) || strings.Contains(ref, "..") || strings.Contains(ref, "//") || strings.HasSuffix(ref, "/") {
		return "", errors.New("只允许绑定合法的 refs/heads/<branch>")
	}
	return ref, nil
}

func BindProject(root, role, ref string) (map[string]any, error) {
	if role != "orchestrator" && role != "verifier" {
		return nil, errors.New("role 必须是 orchestrator 或 verifier")
	}
	root, _ = filepath.Abs(root)
	repository, err := canonicalRepo(root)
	if err != nil {
		return nil, err
	}
	if ref == "" {
		ref, err = currentRef(root)
		if err != nil {
			return nil, err
		}
	}
	if !branchRef.MatchString(ref) || strings.Contains(ref, "..") || strings.Contains(ref, "//") || strings.HasSuffix(ref, "/") {
		return nil, errors.New("只允许绑定合法的 refs/heads/<branch>")
	}
	config := map[string]any{"schema_version": 1, "repository": repository, "ref": ref, "task_path": "tasks/**", "role": role, "created_at": now()}
	name := "project.json"
	if role == "verifier" {
		name = "verifier.json"
	}
	if err := writePrivateJSON(filepath.Join(root, newMeta, name), config); err != nil {
		return nil, err
	}
	for _, dir := range []string{"tasks", "receipts"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			return nil, err
		}
	}
	return config, nil
}

func writePrivateJSON(path string, value any) error {
	return atomicJSON(path, value)
}

func inviteSignature(payload map[string]any, secret string) string {
	canonical, _ := json.Marshal(payload)
	// Map keys are sorted by encoding/json, making this stable across hosts.
	mac := hmacSHA256([]byte(secret), canonical)
	return fmt.Sprintf("%x", mac)
}

func hmacSHA256(key, value []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(value)
	return mac.Sum(nil)
}

func CreateInvite(root string, expiresMinutes int, oneTime bool) (map[string]any, error) {
	if expiresMinutes < 5 || expiresMinutes > 1440 {
		return nil, errors.New("邀请有效期必须在 5 到 1440 分钟之间")
	}
	data, err := os.ReadFile(filepath.Join(root, newMeta, "project.json"))
	if err != nil {
		return nil, errors.New("当前工程不是有效的 orchestrator 绑定")
	}
	var project map[string]any
	if err := json.Unmarshal(data, &project); err != nil || project["role"] != "orchestrator" {
		return nil, errors.New("当前工程不是有效的 orchestrator 绑定")
	}
	ref, _ := project["ref"].(string)
	repository, _ := project["repository"].(string)
	if !branchRef.MatchString(ref) || repository == "" {
		return nil, errors.New("绑定配置非法")
	}
	nonce := randomToken(18)
	payload := map[string]any{"v": 1, "repository": repository, "ref": ref, "task_path": "tasks/**", "mode": "codex", "nonce": nonce, "one_time": oneTime, "expires_at": time.Now().UTC().Add(time.Duration(expiresMinutes) * time.Minute).Truncate(time.Second).Format(time.RFC3339)}
	if secret := os.Getenv("CODE_RELAY_INVITE_SECRET"); secret != "" {
		if len(secret) < 32 {
			return nil, errors.New("CODE_RELAY_INVITE_SECRET 至少需要 32 个字符")
		}
		payload["signature"] = inviteSignature(payload, secret)
	}
	raw, _ := json.Marshal(payload)
	token := strings.TrimRight(base64.RawURLEncoding.EncodeToString(raw), "=")
	joinURL := "code-relay://join/" + token
	if err := writePrivateJSON(filepath.Join(root, newMeta, "invitations", nonce+".json"), payload); err != nil {
		return nil, err
	}
	payload["url"] = joinURL
	return payload, nil
}

func randomToken(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func DecodeInvite(value string) (map[string]any, error) {
	if len(value) > 16*1024 || !strings.HasPrefix(value, "code-relay://join/") {
		return nil, errors.New("无效的 Code Relay 加入链接")
	}
	token := strings.TrimSuffix(strings.TrimPrefix(value, "code-relay://join/"), "/")
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, errors.New("无效的 Code Relay 加入链接")
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, errors.New("无效的 Code Relay 加入链接")
	}
	if payload["v"] != float64(1) || payload["mode"] != "codex" {
		return nil, errors.New("加入链接缺少必要绑定信息")
	}
	ref, _ := payload["ref"].(string)
	nonce, _ := payload["nonce"].(string)
	repository, _ := payload["repository"].(string)
	if !branchRef.MatchString(ref) || !inviteNonce.MatchString(nonce) || repository == "" || payload["task_path"] != "tasks/**" {
		return nil, errors.New("加入链接包含不支持的绑定范围")
	}
	if expires, ok := payload["expires_at"].(string); ok {
		when, parseErr := time.Parse(time.RFC3339, expires)
		if parseErr != nil || when.Before(time.Now().UTC()) {
			return nil, errors.New("加入链接已过期")
		}
	}
	if signature, ok := payload["signature"].(string); ok {
		secret := os.Getenv("CODE_RELAY_INVITE_SECRET")
		if len(secret) < 32 {
			return nil, errors.New("该邀请需要配置 CODE_RELAY_INVITE_SECRET")
		}
		unsigned := map[string]any{}
		for key, item := range payload {
			if key != "signature" {
				unsigned[key] = item
			}
		}
		if !constantTimeEqual(signature, inviteSignature(unsigned, secret)) {
			return nil, errors.New("加入链接签名校验失败")
		}
	}
	return payload, nil
}

func JoinVerifier(root, invite string) (map[string]any, error) {
	payload, err := DecodeInvite(invite)
	if err != nil {
		return nil, err
	}
	root, _ = filepath.Abs(root)
	repository, err := canonicalRepo(root)
	if err != nil {
		return nil, err
	}
	if repository != payload["repository"] {
		return nil, errors.New("当前工程 remote 不匹配")
	}
	ref, err := currentRef(root)
	if err != nil || ref != payload["ref"] {
		return nil, errors.New("当前工程分支不匹配")
	}
	config := map[string]any{"schema_version": 1, "verifier_id": "b-" + randomToken(4), "repository": payload["repository"], "ref": payload["ref"], "task_path": "tasks/**", "mode": "codex", "runtime": "go", "joined_at": now()}
	consumedPath := filepath.Join(root, newMeta, "consumed-invites.json")
	var consumed []string
	_ = readJSON(consumedPath, &consumed)
	for _, item := range consumed {
		if item == payload["nonce"] {
			return nil, errors.New("加入链接已被使用")
		}
	}
	if err := writePrivateJSON(filepath.Join(root, newMeta, "verifier.json"), config); err != nil {
		return nil, err
	}
	if oneTime, ok := payload["one_time"].(bool); !ok || oneTime {
		consumed = append(consumed, payload["nonce"].(string))
		if len(consumed) > 10000 {
			consumed = consumed[len(consumed)-10000:]
		}
		if err := atomicJSON(consumedPath, consumed); err != nil {
			return nil, err
		}
	}
	for _, dir := range []string{"tasks", "receipts"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			return nil, err
		}
	}
	return config, nil
}

func CloneAndJoin(root, invite, destination string) (map[string]any, error) {
	payload, err := DecodeInvite(invite)
	if err != nil {
		return nil, err
	}
	target, err := filepath.Abs(destination)
	if err != nil {
		return nil, err
	}
	if info, statErr := os.Lstat(target); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("拒绝在符号链接目录中自动克隆")
		}
		entries, readErr := os.ReadDir(target)
		if readErr != nil {
			return nil, readErr
		}
		if len(entries) != 0 {
			return nil, fmt.Errorf("目标目录非空，拒绝自动克隆: %s", target)
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return nil, err
	}
	branch := strings.TrimPrefix(payload["ref"].(string), "refs/heads/")
	if _, err := runGit(root, gitTimeout*4, "clone", "--branch", branch, "--single-branch", payload["repository"].(string), target); err != nil {
		return nil, err
	}
	return JoinVerifier(target, invite)
}

func PublishTask(root, markdown string, force, noGit bool) (map[string]any, error) {
	task, err := parseTask(markdown)
	if err != nil {
		return nil, err
	}
	destination := filepath.Join(root, "tasks", task.ID, "task.md")
	if existing, readErr := os.ReadFile(destination); readErr == nil && !force && string(existing) != markdown {
		return nil, fmt.Errorf("任务已存在且内容不同: %s", task.ID)
	}
	if err := atomicJSONText(destination, []byte(markdown)); err != nil {
		return nil, err
	}
	if !noGit {
		if _, err := runGit(root, gitTimeout, "add", filepath.Join("tasks", task.ID, "task.md")); err != nil {
			return nil, err
		}
		if _, err := runGit(root, gitTimeout, "commit", "-m", "code-relay: publish "+task.ID); err != nil && !strings.Contains(strings.ToLower(err.Error()), "nothing to commit") {
			return nil, err
		}
		_, _ = runGit(root, gitTimeout, "push")
	}
	return map[string]any{"task_id": task.ID, "path": destination, "source_commit": task.SourceCommit}, nil
}

func FetchReceipt(root, id string) (Receipt, error) {
	path := filepath.Join(root, "receipts", id, "receipt.json")
	var receipt Receipt
	if err := readJSON(path, &receipt); err != nil {
		return receipt, err
	}
	taskRaw, err := os.ReadFile(filepath.Join(root, "tasks", id, "task.md"))
	if err != nil {
		return receipt, err
	}
	task, err := parseTask(string(taskRaw))
	if err != nil {
		return receipt, err
	}
	return receipt, validateReceipt(receipt, task)
}

func Analyze(root, id string) (map[string]any, error) {
	receipt, err := FetchReceipt(root, id)
	if err != nil {
		return nil, err
	}
	failed := []Check{}
	for _, check := range receipt.Checks {
		if check.Status != "passed" {
			failed = append(failed, check)
		}
	}
	if receipt.Status == "passed" && len(failed) == 0 {
		return map[string]any{"task_id": id, "conclusion": "done", "summary": "B 验证全部通过，任务完成。", "next_actions": receipt.NextActions}, nil
	}
	if receipt.Status == "blocked" {
		return map[string]any{"task_id": id, "conclusion": "blocked", "summary": "B 验证被阻塞，需要用户决策。", "next_actions": receipt.NextActions}, nil
	}
	return map[string]any{"task_id": id, "conclusion": "iterate", "summary": fmt.Sprintf("B 验证失败 %d 项，建议启动下一轮任务。", len(failed)), "failed_checks": failed, "next_actions": receipt.NextActions}, nil
}

func RunPending(root string, timeout int) ([]map[string]any, error) {
	base := filepath.Join(root, "tasks")
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return []map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() && taskID.MatchString(entry.Name()) {
			if _, err := os.Stat(filepath.Join(root, "receipts", entry.Name(), "receipt.json")); os.IsNotExist(err) {
				ids = append(ids, entry.Name())
			}
		}
	}
	sort.Strings(ids)
	results := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		worktree := filepath.Join(root, newMeta, "worktrees", id)
		_ = os.RemoveAll(worktree)
		_, _ = runGit(root, gitTimeout, "worktree", "add", "--detach", worktree, taskSourceCommit(root, id))
		receipt, runErr := RunTask(root, id, timeout, worktree)
		if runErr == nil {
			runErr = PersistReceipt(root, receipt)
		}
		_ = runGitIgnore(root, "worktree", "remove", "--force", worktree)
		row := map[string]any{"task_id": id, "status": receipt.Status}
		if runErr != nil {
			row["error"] = runErr.Error()
		}
		results = append(results, row)
	}
	return results, nil
}

func taskSourceCommit(root, id string) string {
	raw, _ := os.ReadFile(filepath.Join(root, "tasks", id, "task.md"))
	task, _ := parseTask(string(raw))
	return task.SourceCommit
}

func runGitIgnore(root string, args ...string) error {
	_, err := runGit(root, gitTimeout, args...)
	return err
}

func PublishReceipts(root string) error {
	if _, err := runGit(root, gitTimeout, "add", "receipts"); err != nil {
		return err
	}
	if _, err := runGit(root, gitTimeout, "commit", "-m", "code-relay: publish verification receipts"); err != nil && !strings.Contains(strings.ToLower(err.Error()), "nothing to commit") {
		return err
	}
	_, err := runGit(root, gitTimeout, "push")
	return err
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
