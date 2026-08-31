package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// RemoteMCPBackend is the data-plane boundary used by the hosted gateway.
// Implementations must use remote APIs only; no local checkout or container
// filesystem is involved in task or receipt operations.
type RemoteMCPBackend interface {
	Call(context.Context, OAuthSession, string, map[string]any) (any, error)
}

type GitHubRemoteBackend struct {
	App      *GitHubAppClient
	Workflow string
	// AllowedRefs optionally restricts repository/ref pairs. An empty map
	// preserves GitHub App repository authorization while still requiring an
	// explicit branch on every request.
	AllowedRefs map[string]bool
}

func NewGitHubRemoteBackend(app *GitHubAppClient, workflow string) (*GitHubRemoteBackend, error) {
	if app == nil {
		return nil, errors.New("GitHub App client is required")
	}
	if strings.TrimSpace(workflow) == "" {
		workflow = "verify-on-b.yml"
	}
	return &GitHubRemoteBackend{App: app, Workflow: workflow, AllowedRefs: map[string]bool{}}, nil
}

func (b *GitHubRemoteBackend) SetAllowedRefs(values []string) {
	if b.AllowedRefs == nil {
		b.AllowedRefs = map[string]bool{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			b.AllowedRefs[value] = true
		}
	}
}

func (b *GitHubRemoteBackend) Call(ctx context.Context, session OAuthSession, name string, args map[string]any) (any, error) {
	repo, repository, ref, err := b.authorizedRepository(ctx, session, args)
	if err != nil {
		return nil, err
	}
	switch name {
	case "bind_project":
		role := stringArg(args, "role")
		if role != "orchestrator" && role != "verifier" {
			return nil, errors.New("role 必须是 orchestrator 或 verifier")
		}
		if err := repo.GetRef(ctx, repository, ref); err != nil {
			return nil, fmt.Errorf("绑定分支不可访问: %w", err)
		}
		return map[string]any{"schema_version": 1, "repository": repository, "ref": ref, "task_path": "tasks/**", "role": role, "subject": session.Subject, "login": session.Login, "installation_id": session.InstallationID, "created_at": now()}, nil
	case "doctor":
		if err := repo.GetRef(ctx, repository, ref); err != nil {
			return nil, err
		}
		return map[string]any{"status": "ok", "repository": repository, "ref": ref, "github_app": true, "user": session.Login, "checked_at": now()}, nil
	case "publish_task":
		markdown, ok := args["markdown"].(string)
		if !ok || strings.TrimSpace(markdown) == "" {
			return nil, errors.New("markdown is required")
		}
		task, err := parseTask(markdown)
		if err != nil {
			return nil, err
		}
		path := "tasks/" + task.ID + "/task.md"
		if old, _, getErr := repo.GetContent(ctx, repository, path, ref); getErr == nil && string(old) != markdown && !boolDefault(args["force"], false) {
			return nil, fmt.Errorf("任务已存在且内容不同: %s", task.ID)
		}
		_, sha, getErr := repo.GetContent(ctx, repository, path, ref)
		if getErr != nil && !strings.Contains(getErr.Error(), "404") {
			return nil, getErr
		}
		if err := repo.PutContent(ctx, repository, path, ref, "code-relay: publish "+task.ID, []byte(markdown), sha); err != nil {
			return nil, err
		}
		dispatched := false
		if boolDefault(args["dispatch"], true) {
			workflow := stringArg(args, "workflow")
			if workflow == "" {
				workflow = b.Workflow
			}
			if err := repo.DispatchWorkflow(ctx, repository, workflow, ref, map[string]string{"task_id": task.ID}); err != nil {
				return nil, fmt.Errorf("任务已提交但 workflow dispatch 失败: %w", err)
			}
			dispatched = true
		}
		return map[string]any{"task_id": task.ID, "repository": repository, "ref": ref, "source_commit": task.SourceCommit, "dispatched": dispatched, "storage": "github-api"}, nil
	case "fetch_receipt":
		id := stringArg(args, "task_id")
		return b.fetchReceipt(ctx, repo, repository, ref, id)
	case "analyze":
		id := stringArg(args, "task_id")
		receipt, err := b.fetchReceipt(ctx, repo, repository, ref, id)
		if err != nil {
			return nil, err
		}
		return analyzeReceiptValue(id, receipt), nil
	case "status":
		return b.status(ctx, repo, repository, ref)
	default:
		return nil, fmt.Errorf("remote tool is not supported: %s", name)
	}
}

func (b *GitHubRemoteBackend) authorizedRepository(ctx context.Context, session OAuthSession, args map[string]any) (*GitHubRepositoryClient, string, string, error) {
	repository, err := normalizeRepository(stringArg(args, "repository"))
	if err != nil {
		return nil, "", "", err
	}
	ref, err := normalizeRef(stringArg(args, "ref"))
	if err != nil {
		return nil, "", "", err
	}
	if len(b.AllowedRefs) > 0 && !b.AllowedRefs[repository+"@"+ref] && !b.AllowedRefs[ref] {
		return nil, "", "", errors.New("repository/ref is not allowed by the hosted policy")
	}
	if session.InstallationID <= 0 {
		return nil, "", "", errors.New("请先完成 GitHub App 安装并绑定 installation")
	}
	if err := b.App.UserCanAccessRepository(ctx, session.AccessToken, session.InstallationID, repository); err != nil {
		return nil, "", "", err
	}
	repo, err := b.App.Repository(ctx, session.InstallationID, repository)
	if err != nil {
		return nil, "", "", err
	}
	return repo, repository, ref, nil
}

func (b *GitHubRemoteBackend) fetchReceipt(ctx context.Context, repo *GitHubRepositoryClient, repository, ref, id string) (Receipt, error) {
	if err := validateTaskID(id); err != nil {
		return Receipt{}, err
	}
	receiptRaw, _, err := repo.GetContent(ctx, repository, "receipts/"+id+"/receipt.json", ref)
	if err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	if err := jsonUnmarshalLimited(receiptRaw, &receipt); err != nil {
		return Receipt{}, err
	}
	taskRaw, _, err := repo.GetContent(ctx, repository, "tasks/"+id+"/task.md", ref)
	if err != nil {
		return receipt, err
	}
	task, err := parseTask(string(taskRaw))
	if err != nil {
		return receipt, err
	}
	return receipt, validateReceipt(receipt, task)
}

func (b *GitHubRemoteBackend) status(ctx context.Context, repo *GitHubRepositoryClient, repository, ref string) ([]map[string]any, error) {
	tasks, err := repo.ListDirectory(ctx, repository, "tasks", ref)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(tasks))
	for _, entry := range tasks {
		if entry.Type != "dir" || !taskID.MatchString(entry.Name) {
			continue
		}
		row := map[string]any{"task_id": entry.Name, "status": "pending"}
		if receipt, fetchErr := b.fetchReceipt(ctx, repo, repository, ref, entry.Name); fetchErr == nil {
			row["status"] = receipt.Status
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func analyzeReceiptValue(id string, receipt Receipt) map[string]any {
	failed := make([]Check, 0)
	for _, check := range receipt.Checks {
		if check.Status != "passed" {
			failed = append(failed, check)
		}
	}
	if receipt.Status == "passed" && len(failed) == 0 {
		return map[string]any{"task_id": id, "conclusion": "done", "summary": "B 验证全部通过，任务完成。", "next_actions": receipt.NextActions}
	}
	if receipt.Status == "blocked" {
		return map[string]any{"task_id": id, "conclusion": "blocked", "summary": "B 验证被阻塞，需要用户决策。", "next_actions": receipt.NextActions}
	}
	return map[string]any{"task_id": id, "conclusion": "iterate", "summary": fmt.Sprintf("B 验证失败 %d 项，建议启动下一轮任务。", len(failed)), "failed_checks": failed, "next_actions": receipt.NextActions}
}

func normalizeRef(value string) (string, error) {
	if value == "" {
		return "", errors.New("ref is required; use refs/heads/<branch>")
	}
	if strings.HasPrefix(value, "refs/") && !strings.HasPrefix(value, "refs/heads/") {
		return "", errors.New("only refs/heads/<branch> is supported")
	}
	if !strings.HasPrefix(value, "refs/heads/") {
		value = "refs/heads/" + strings.TrimPrefix(value, "heads/")
	}
	if !branchRef.MatchString(value) || strings.Contains(value, "..") || strings.Contains(value, "//") || strings.HasSuffix(value, "/") {
		return "", errors.New("ref must be a valid refs/heads/<branch>")
	}
	return value, nil
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func jsonUnmarshalLimited(data []byte, out any) error {
	if len(data) > maxTask {
		return errors.New("remote JSON exceeds size limit")
	}
	return json.Unmarshal(data, out)
}
