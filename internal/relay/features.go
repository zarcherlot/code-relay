package relay

// This file contains the user-facing orchestration operations.  They are
// deliberately implemented in the same Go binary as the checkpoint so the
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

const (
	bindingSchemaVersion = 2
	inviteVersion        = 3
	inviteMode           = "mcp"
)

var inviteNonce = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

func canonicalRepo(root string) (string, error) {
	out, err := runGit(root, gitTimeout, "config", "--get", "remote.origin.url")
	if err != nil {
		return "", errors.New("无法读取 origin remote")
	}
	return sanitizeRemote(string(out))
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
	if role != "orchestrator" && role != "checkpoint" {
		return nil, errors.New("role 必须是 orchestrator 或 checkpoint")
	}
	root, err := pathWithinRoot(root, root)
	if err != nil {
		return nil, err
	}
	prefix, err := runGit(root, gitTimeout, "rev-parse", "--show-prefix")
	if err != nil || strings.TrimSpace(string(prefix)) != "" {
		return nil, errors.New("绑定目录必须是 Git 工程根目录")
	}
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
	config := map[string]any{"schema_version": bindingSchemaVersion, "repository": repository, "ref": ref, "runbook_path": "runbooks/**", "role": role, "created_at": now()}
	name := "project.json"
	if role == "checkpoint" {
		name = "checkpoint.json"
	}
	configPath, err := projectPath(root, newMeta, name)
	if err != nil {
		return nil, err
	}
	if err := writePrivateJSON(configPath, config); err != nil {
		return nil, err
	}
	for _, dir := range []string{"runbooks", "receipts"} {
		path, pathErr := projectPath(root, dir)
		if pathErr != nil {
			return nil, pathErr
		}
		if err := ensurePrivateDir(path); err != nil {
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
	bindingPath, err := projectPath(root, newMeta, "project.json")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(bindingPath)
	if err != nil {
		return nil, errors.New("当前工程不是有效的 orchestrator 绑定")
	}
	var project map[string]any
	if err := json.Unmarshal(data, &project); err != nil || project["schema_version"] != float64(bindingSchemaVersion) || project["role"] != "orchestrator" || project["runbook_path"] != "runbooks/**" {
		return nil, errors.New("当前工程不是有效的 orchestrator 绑定")
	}
	ref, _ := project["ref"].(string)
	repository, _ := project["repository"].(string)
	if !branchRef.MatchString(ref) || repository == "" {
		return nil, errors.New("绑定配置非法")
	}
	nonce := randomToken(18)
	payload := map[string]any{"v": inviteVersion, "repository": repository, "ref": ref, "runbook_path": "runbooks/**", "mode": inviteMode, "nonce": nonce, "one_time": oneTime, "expires_at": time.Now().UTC().Add(time.Duration(expiresMinutes) * time.Minute).Truncate(time.Second).Format(time.RFC3339)}
	if secret := os.Getenv("CODE_RELAY_INVITE_SECRET"); secret != "" {
		if len(secret) < 32 {
			return nil, errors.New("CODE_RELAY_INVITE_SECRET 至少需要 32 个字符")
		}
		payload["signature"] = inviteSignature(payload, secret)
	}
	raw, _ := json.Marshal(payload)
	token := strings.TrimRight(base64.RawURLEncoding.EncodeToString(raw), "=")
	joinURL := "code-relay://join/" + token
	invitePath, err := projectPath(root, newMeta, "invitations", nonce+".json")
	if err != nil {
		return nil, err
	}
	if err := writePrivateJSON(invitePath, payload); err != nil {
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
	if payload["v"] != float64(inviteVersion) || payload["mode"] != inviteMode {
		return nil, errors.New("加入链接缺少必要绑定信息")
	}
	ref, _ := payload["ref"].(string)
	nonce, _ := payload["nonce"].(string)
	repository, _ := payload["repository"].(string)
	if !branchRef.MatchString(ref) || !inviteNonce.MatchString(nonce) || repository == "" || payload["runbook_path"] != "runbooks/**" {
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

func JoinCheckpoint(root, invite string) (map[string]any, error) {
	payload, err := DecodeInvite(invite)
	if err != nil {
		return nil, err
	}
	root, err = pathWithinRoot(root, root)
	if err != nil {
		return nil, err
	}
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
	config := map[string]any{"schema_version": bindingSchemaVersion, "checkpoint_id": "checkpoint-" + randomToken(4), "repository": payload["repository"], "ref": payload["ref"], "runbook_path": "runbooks/**", "mode": inviteMode, "runtime": "go", "joined_at": now()}
	// Serialize the consumed-invite read/check/write sequence so a one-time
	// nonce cannot be accepted by two concurrent join requests.
	inviteLock, lockErr := acquireRunbookLock(root, "invite-consumption", 10*time.Second)
	if lockErr != nil {
		return nil, lockErr
	}
	defer inviteLock.release()
	consumedPath, err := projectPath(root, newMeta, "consumed-invites.json")
	if err != nil {
		return nil, err
	}
	var consumed []string
	_ = readJSON(consumedPath, &consumed)
	for _, item := range consumed {
		if item == payload["nonce"] {
			return nil, errors.New("加入链接已被使用")
		}
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
	checkpointPath, err := projectPath(root, newMeta, "checkpoint.json")
	if err != nil {
		return nil, err
	}
	if err := writePrivateJSON(checkpointPath, config); err != nil {
		return nil, err
	}
	for _, dir := range []string{"runbooks", "receipts"} {
		path, pathErr := projectPath(root, dir)
		if pathErr != nil {
			return nil, pathErr
		}
		if err := ensurePrivateDir(path); err != nil {
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
	return JoinCheckpoint(target, invite)
}

func PublishRunbook(root, markdown string, force, noGit bool) (map[string]any, error) {
	runbook, err := parseRunbook(markdown)
	if err != nil {
		return nil, err
	}
	destination, err := projectPath(root, "runbooks", runbook.ID, "runbook.md")
	if err != nil {
		return nil, err
	}
	if existing, readErr := os.ReadFile(destination); readErr == nil && !force && string(existing) != markdown {
		return nil, fmt.Errorf("runbook 已存在且内容不同: %s", runbook.ID)
	}
	if err := atomicJSONText(destination, []byte(markdown)); err != nil {
		return nil, err
	}
	if !noGit {
		if _, err := runGit(root, gitTimeout, "add", filepath.Join("runbooks", runbook.ID, "runbook.md")); err != nil {
			return nil, err
		}
		changed, err := stagedChanges(root, filepath.Join("runbooks", runbook.ID, "runbook.md"))
		if err != nil {
			return nil, err
		}
		if changed {
			if _, err := runGit(root, gitTimeout, "commit", "-m", "code-relay: publish "+runbook.ID); err != nil {
				return nil, err
			}
		}
		if _, err := runGit(root, gitTimeout, "push"); err != nil {
			return nil, err
		}
	}
	return map[string]any{"runbook_id": runbook.ID, "path": destination, "source_commit": runbook.SourceCommit}, nil
}

func FetchReceipt(root, id string) (Receipt, error) {
	if err := validateRunbookID(id); err != nil {
		return Receipt{}, err
	}
	path, err := projectPath(root, "receipts", id, "receipt.json")
	if err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	if err := readJSON(path, &receipt); err != nil {
		return receipt, err
	}
	runbookPath, err := projectPath(root, "runbooks", id, "runbook.md")
	if err != nil {
		return receipt, err
	}
	runbookRaw, err := os.ReadFile(runbookPath)
	if err != nil {
		return receipt, err
	}
	runbook, err := parseRunbook(string(runbookRaw))
	if err != nil {
		return receipt, err
	}
	return receipt, validateReceipt(receipt, runbook)
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
		return map[string]any{"runbook_id": id, "conclusion": "done", "summary": "B 验证全部通过，runbook 完成。", "next_actions": receipt.NextActions}, nil
	}
	if receipt.Status == "blocked" {
		return map[string]any{"runbook_id": id, "conclusion": "blocked", "summary": "B 验证被阻塞，需要用户决策。", "next_actions": receipt.NextActions}, nil
	}
	return map[string]any{"runbook_id": id, "conclusion": "iterate", "summary": fmt.Sprintf("B 验证失败 %d 项，建议发布下一版 runbook。", len(failed)), "failed_checks": failed, "next_actions": receipt.NextActions}, nil
}

func RunPending(root string, timeout int) ([]map[string]any, error) {
	root, err := pathWithinRoot(root, root)
	if err != nil {
		return nil, err
	}
	pendingLock, err := acquireRunbookLock(root, "run-pending", 10*time.Second)
	if err != nil {
		return nil, err
	}
	defer pendingLock.release()
	base, err := projectPath(root, "runbooks")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return []map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() && runbookID.MatchString(entry.Name()) {
			runbook, runbookErr := readRunbook(root, entry.Name())
			receiptPath, pathErr := projectPath(root, "receipts", entry.Name(), "receipt.json")
			var receipt Receipt
			receiptErr := pathErr
			if receiptErr == nil {
				receiptErr = readJSON(receiptPath, &receipt)
			}
			if runbookErr != nil || receiptErr != nil || validateReceipt(receipt, runbook) != nil {
				ids = append(ids, entry.Name())
			}
		}
	}
	sort.Strings(ids)
	results := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		runbook, runbookErr := readRunbook(root, id)
		if runbookErr != nil {
			receipt := Receipt{RunbookID: id, Status: "blocked", Checks: []Check{{Name: "Runbook 协议", Expected: "runbook.md 通过协议校验", Actual: runbookErr.Error(), Status: "blocked"}}, Risks: []string{"runbook.md 不符合 Code Relay 协议"}, NextActions: []string{"修正 runbook.md 后重新发布"}, VerifiedAt: now(), Environment: map[string]string{"platform": "protocol"}}
			persistErr := PersistReceipt(root, receipt)
			row := map[string]any{"runbook_id": id, "status": receipt.Status}
			if persistErr != nil {
				row["error"] = persistErr.Error()
			}
			results = append(results, row)
			continue
		}
		worktree, pathErr := projectPath(root, newMeta, "worktrees", id)
		if pathErr != nil {
			return nil, pathErr
		}
		if err := ensurePrivateDir(filepath.Dir(worktree)); err != nil {
			return nil, err
		}
		if cleanupErr := cleanupWorktree(root, worktree); cleanupErr != nil {
			return nil, fmt.Errorf("清理旧 worktree 失败: %w", cleanupErr)
		}
		if _, statErr := os.Stat(worktree); statErr == nil {
			if removeErr := os.RemoveAll(worktree); removeErr != nil {
				return nil, removeErr
			}
		}
		if _, addErr := runGit(root, gitTimeout, "worktree", "add", "--detach", worktree, runbook.SourceCommit); addErr != nil {
			receipt := Receipt{RunbookID: runbook.ID, SourceCommit: runbook.SourceCommit, Status: "blocked", Checks: []Check{{Name: "隔离 worktree", Expected: "可以检出 source_commit", Actual: addErr.Error(), Status: "blocked"}}, Risks: []string{"无法验证指定 source commit"}, NextActions: []string{"确认 source_commit 已推送且可访问"}, VerifiedAt: now(), Environment: map[string]string{"platform": "git"}, RunbookSHA256: sha256Hex([]byte(runbook.Raw))}
			persistErr := PersistReceipt(root, receipt)
			row := map[string]any{"runbook_id": id, "status": receipt.Status}
			if persistErr != nil {
				row["error"] = persistErr.Error()
			}
			results = append(results, row)
			continue
		}
		receipt, runErr := RunRunbook(root, id, timeout, worktree)
		if runErr == nil {
			runErr = PersistReceipt(root, receipt)
		}
		cleanupErr := cleanupWorktree(root, worktree)
		status := receipt.Status
		if runErr != nil {
			status = "error"
		}
		row := map[string]any{"runbook_id": id, "status": status}
		if runErr != nil {
			row["error"] = runErr.Error()
		} else if cleanupErr != nil {
			row["cleanup_error"] = cleanupErr.Error()
		}
		results = append(results, row)
	}
	return results, nil
}

func cleanupWorktree(root, worktree string) error {
	// Git may already have removed the worktree after an interrupted run. Keep
	// that case idempotent, then prune stale administrative entries.
	removeErr := runGitIgnore(root, "worktree", "remove", "--force", worktree)
	if removeErr != nil {
		if _, statErr := os.Stat(worktree); statErr == nil {
			return removeErr
		}
	}
	return runGitIgnore(root, "worktree", "prune")
}

func readRunbook(root, id string) (Runbook, error) {
	if err := validateRunbookID(id); err != nil {
		return Runbook{}, err
	}
	path, err := projectPath(root, "runbooks", id, "runbook.md")
	if err != nil {
		return Runbook{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Runbook{}, err
	}
	return parseRunbook(string(raw))
}

func runGitIgnore(root string, args ...string) error {
	_, err := runGit(root, gitTimeout, args...)
	return err
}

func stagedChanges(root string, paths ...string) (bool, error) {
	args := []string{"diff", "--cached", "--name-only", "--"}
	args = append(args, paths...)
	out, err := runGit(root, gitTimeout, args...)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func PublishReceipts(root string) error {
	root, err := pathWithinRoot(root, root)
	if err != nil {
		return err
	}
	publishLock, err := acquireRunbookLock(root, "publish-receipts", 10*time.Second)
	if err != nil {
		return err
	}
	defer publishLock.release()
	ref, err := receiptPublishRef(root)
	if err != nil {
		return err
	}
	if _, err := runGit(root, gitTimeout, "add", "receipts"); err != nil {
		return err
	}
	changed, err := stagedChanges(root, "receipts")
	if err != nil {
		return err
	}
	if changed {
		if _, err := runGit(root, gitTimeout, "commit", "-m", "code-relay: publish verification receipts"); err != nil {
			return err
		}
	}
	_, err = runGit(root, gitTimeout, "push", "origin", "HEAD:"+ref)
	return err
}

func receiptPublishRef(root string) (string, error) {
	configured := ""
	checkpointPath, err := projectPath(root, newMeta, "checkpoint.json")
	if err != nil {
		return "", err
	}
	var checkpoint map[string]any
	if readJSON(checkpointPath, &checkpoint) == nil {
		configured, _ = checkpoint["ref"].(string)
	}
	environmentRef := os.Getenv("GITHUB_REF")
	if environmentRef != "" && configured != "" && environmentRef != configured {
		return "", errors.New("GITHUB_REF 与 checkpoint 绑定分支不匹配")
	}
	ref := environmentRef
	if ref == "" {
		ref = configured
	}
	if ref == "" {
		ref, err = currentRef(root)
		if err != nil {
			return "", err
		}
	}
	if !branchRef.MatchString(ref) || strings.Contains(ref, "..") || strings.Contains(ref, "//") || strings.HasSuffix(ref, "/") {
		return "", errors.New("无法确定安全的 receipt 发布分支")
	}
	return ref, nil
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
