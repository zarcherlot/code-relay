package relay

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const testRunbook = `# Runbook
- runbook_id: go-test
- source_commit: abc1234
- target: B
- objective: smoke

## Validation Plan
1. go version

## Expected Results
- exits successfully
`

func TestParseRunbookAndRun(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "runbooks", "go-test", "runbook.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(testRunbook), 0600); err != nil {
		t.Fatal(err)
	}
	receipt, err := RunRunbook(root, "go-test", 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "passed" || receipt.RunbookID != "go-test" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestUnsafeCommandBlocked(t *testing.T) {
	if _, err := parseCommand("echo ok && echo bad"); err == nil {
		t.Fatal("expected shell operator rejection")
	}
	if _, err := parseCommand("rm -rf /"); err == nil {
		t.Fatal("expected deny-list rejection")
	}
	for _, command := range []string{"bash -c echo", "powershell -Command whoami", "node -e console.log(1)", "npm exec whoami"} {
		if _, err := parseCommand(command); err == nil {
			t.Fatalf("expected interpreter escape rejection: %s", command)
		}
	}
}

func TestRunbookIDTraversalRejected(t *testing.T) {
	root := t.TempDir()
	if _, err := RunRunbook(root, "..", 5, ""); err == nil {
		t.Fatal("expected run-runbook traversal rejection")
	}
	if _, err := FetchReceipt(root, "../outside"); err == nil {
		t.Fatal("expected fetch traversal rejection")
	}
}

func TestRemoteCredentialsAreRemoved(t *testing.T) {
	remote, err := sanitizeRemote("https://x-access-token:secret@github.com/example/relay.git")
	if err != nil {
		t.Fatal(err)
	}
	if remote != "https://github.com/example/relay" || strings.Contains(remote, "secret") {
		t.Fatalf("remote was not sanitized: %s", remote)
	}
	redacted := redactSensitive("fatal: https://token@example.com/repo")
	if strings.Contains(redacted, "token@") {
		t.Fatalf("credential was not redacted: %s", redacted)
	}
	for _, unsafe := range []string{"https://github.com/owner", "https://github.com/owner/repo%0Ainjected"} {
		if _, err := sanitizeRemote(unsafe); err == nil {
			t.Fatalf("unsafe remote was accepted: %s", unsafe)
		}
	}
}

func TestGitEnvironmentRemovesRedirectAndSecretVariables(t *testing.T) {
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "outside"))
	t.Setenv("GIT_SSH_COMMAND", "ssh -o ProxyCommand=unsafe")
	t.Setenv("GITHUB_TOKEN", "test-token")
	env := gitEnvironment()
	for _, entry := range env {
		key := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		if restrictedGitEnvKeys[key] || sensitiveEnvKeys[key] {
			t.Fatalf("restricted environment variable leaked: %s", key)
		}
	}
	foundPrompt := false
	for _, entry := range env {
		if entry == "GIT_TERMINAL_PROMPT=0" {
			foundPrompt = true
		}
	}
	if !foundPrompt {
		t.Fatal("git prompt prevention variable missing")
	}
}

func TestTailBufferIsBounded(t *testing.T) {
	buffer := newTailBuffer(16)
	_, _ = buffer.Write([]byte("0123456789"))
	_, _ = buffer.Write([]byte("abcdefghij"))
	if got := buffer.String(); got != "456789abcdefghij" {
		t.Fatalf("unexpected tail: %q", got)
	}
}

func TestStatusEmpty(t *testing.T) {
	value, err := Status(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rows, ok := value.([]any)
	if !ok || len(rows) != 0 {
		t.Fatalf("unexpected status: %#v", value)
	}
}

func TestInvalidRunbookProducesBlockedReceipt(t *testing.T) {
	r := t.TempDir()
	p := filepath.Join(r, "runbooks", "broken", "runbook.md")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("# Runbook\n- runbook_id: broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	receipt, err := RunRunbook(r, "broken", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "blocked" || len(receipt.Checks) != 1 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestRunbookLockExcludesConcurrentExecution(t *testing.T) {
	root := t.TempDir()
	first, err := acquireRunbookLock(root, "same-runbook", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()
	if _, err := acquireRunbookLock(root, "same-runbook", 50*time.Millisecond); err == nil {
		t.Fatal("expected busy runbook lock")
	}
}

func TestDoctorReportsWorkspace(t *testing.T) {
	report, err := Doctor(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if report["status"] != "error" {
		t.Fatalf("expected unbound temp directory to report error: %#v", report)
	}
}

func TestPolicyMatchesSharedDocument(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "runtime-policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	var shared map[string]any
	if err := json.Unmarshal(raw, &shared); err != nil {
		t.Fatal(err)
	}
	actual := policyDocument()
	for _, key := range []string{"allowed_commands", "deny_tokens", "shell_operators", "sensitive_env_keys"} {
		want, ok := shared[key].([]any)
		if !ok {
			t.Fatalf("shared policy field %s is not an array", key)
		}
		got := actual[key].([]string)
		values := make([]string, len(want))
		for i, value := range want {
			values[i], ok = value.(string)
			if !ok {
				t.Fatalf("shared policy field %s contains non-string", key)
			}
		}
		if !reflect.DeepEqual(got, values) {
			t.Fatalf("policy mismatch for %s: got %#v want %#v", key, got, values)
		}
	}
	sharedDenied, ok := shared["denied_command_arguments"].(map[string]any)
	if !ok {
		t.Fatal("shared denied_command_arguments is not an object")
	}
	actualDenied := actual["denied_command_arguments"].(map[string][]string)
	for command, rawValues := range sharedDenied {
		items, ok := rawValues.([]any)
		if !ok {
			t.Fatalf("denied arguments for %s are not an array", command)
		}
		values := make([]string, len(items))
		for i, item := range items {
			values[i], ok = item.(string)
			if !ok {
				t.Fatalf("denied argument for %s is not a string", command)
			}
		}
		if !reflect.DeepEqual(actualDenied[command], values) {
			t.Fatalf("denied argument mismatch for %s: got %#v want %#v", command, actualDenied[command], values)
		}
	}
}

func TestExamplesMatchRuntimeProtocol(t *testing.T) {
	markdown, err := os.ReadFile(filepath.Join("..", "..", "examples", "runbook-001.md"))
	if err != nil {
		t.Fatal(err)
	}
	runbook, err := parseRunbook(string(markdown))
	if err != nil {
		t.Fatal(err)
	}
	var runbookJSON struct {
		ID     string `json:"runbook_id"`
		Source string `json:"source_commit"`
	}
	if err := readJSON(filepath.Join("..", "..", "examples", "runbook-001.json"), &runbookJSON); err != nil {
		t.Fatal(err)
	}
	if runbook.ID != runbookJSON.ID || runbook.SourceCommit != runbookJSON.Source {
		t.Fatalf("runbook fixtures disagree: markdown=%+v json=%+v", runbook, runbookJSON)
	}
	var receipt Receipt
	if err := readJSON(filepath.Join("..", "..", "examples", "receipt-001.json"), &receipt); err != nil {
		t.Fatal(err)
	}
	if err := validateReceipt(receipt, runbook); err != nil {
		t.Fatalf("receipt fixture is invalid: %v", err)
	}
}

func TestReceiptBoundsAreEnforced(t *testing.T) {
	runbook, err := parseRunbook(testRunbook)
	if err != nil {
		t.Fatal(err)
	}
	receipt := Receipt{RunbookID: runbook.ID, SourceCommit: runbook.SourceCommit, Status: "passed", Checks: []Check{}, Risks: make([]string, 101)}
	if err := validateReceipt(receipt, runbook); err == nil {
		t.Fatal("expected receipt risk bound rejection")
	}
	receipt.Risks = nil
	receipt.RunbookSHA256 = "not-a-sha"
	if err := validateReceipt(receipt, runbook); err == nil {
		t.Fatal("expected receipt runbook hash rejection")
	}
}

func TestInviteRoundTrip(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	if out, err := exec.Command("git", "-C", root, "config", "remote.origin.url", "https://github.com/example/relay.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote: %v (%s)", err, out)
	}
	if out, err := exec.Command("git", "-C", root, "checkout", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v (%s)", err, out)
	}
	binding, err := BindProject(root, "orchestrator", "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if binding["schema_version"] != bindingSchemaVersion || binding["runbook_path"] != "runbooks/**" {
		t.Fatalf("unexpected binding protocol: %#v", binding)
	}
	invite, err := CreateInvite(root, 30, true)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := DecodeInvite(invite["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if payload["v"] != float64(inviteVersion) || payload["repository"] != "https://github.com/example/relay" || payload["ref"] != "refs/heads/main" {
		t.Fatalf("unexpected invite: %#v", payload)
	}
}

func TestLegacyTaskInviteIsRejected(t *testing.T) {
	payload := map[string]any{
		"v":          1,
		"repository": "https://github.com/example/relay",
		"ref":        "refs/heads/main",
		"task_path":  "tasks/**",
		"mode":       "codex",
		"nonce":      "legacy-invite-token",
		"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	url := "code-relay://join/" + base64.RawURLEncoding.EncodeToString(raw)
	if _, err := DecodeInvite(url); err == nil {
		t.Fatal("expected v1 task invitation to be rejected")
	}
}

func TestOneTimeInviteAllowsOnlyOneConcurrentJoin(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	if out, err := exec.Command("git", "-C", root, "config", "remote.origin.url", "https://github.com/example/relay.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote: %v (%s)", err, out)
	}
	if out, err := exec.Command("git", "-C", root, "checkout", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v (%s)", err, out)
	}
	if _, err := BindProject(root, "orchestrator", "refs/heads/main"); err != nil {
		t.Fatal(err)
	}
	invite, err := CreateInvite(root, 30, true)
	if err != nil {
		t.Fatal(err)
	}
	const attempts = 8
	results := make(chan error, attempts)
	var group sync.WaitGroup
	for i := 0; i < attempts; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, joinErr := JoinCheckpoint(root, invite["url"].(string))
			results <- joinErr
		}()
	}
	group.Wait()
	close(results)
	accepted := 0
	for joinErr := range results {
		if joinErr == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("one-time invite accepted %d times", accepted)
	}
}

func TestMCPToolsAndPublish(t *testing.T) {
	root := t.TempDir()
	markdown := testRunbook
	result, err := PublishRunbook(root, markdown, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if result["runbook_id"] != "go-test" {
		t.Fatalf("unexpected publish result: %#v", result)
	}
	tools := mcpTools()
	if len(tools) < 9 {
		t.Fatalf("expected MCP tools, got %d", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		names[name] = true
	}
	for _, name := range []string{"publish_runbook", "create_checkpoint_invite", "join_checkpoint"} {
		if !names[name] {
			t.Fatalf("missing renamed MCP tool %s", name)
		}
	}
	for _, name := range []string{"publish_task", "create_verifier_invite", "join_verifier"} {
		if names[name] {
			t.Fatalf("legacy MCP tool remains exposed: %s", name)
		}
	}
}

func TestMCPStdioRoundTrip(t *testing.T) {
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{}}\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\",\"params\":{}}\n")
	var output bytes.Buffer
	if err := MCPStdio(input, &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var initialize map[string]any
	if err := decoder.Decode(&initialize); err != nil {
		t.Fatal(err)
	}
	if initialize["jsonrpc"] != "2.0" || initialize["id"].(float64) != 1 {
		t.Fatalf("unexpected initialize response: %#v", initialize)
	}
	var listed map[string]any
	if err := decoder.Decode(&listed); err != nil {
		t.Fatal(err)
	}
	result, ok := listed["result"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected tools response: %#v", listed)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) < 9 {
		t.Fatalf("expected MCP tools, got %#v", result["tools"])
	}
}

func TestMCPRejectsInvalidRequestsAndContinues(t *testing.T) {
	oversized := strings.Repeat("x", 2*maxRunbook+1)
	input := strings.NewReader("{\"jsonrpc\":\"1.0\",\"id\":1,\"method\":\"ping\"}\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{}}\n" +
		oversized + "\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"ping\"}\n")
	var output bytes.Buffer
	if err := MCPStdio(input, &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	wantCodes := []float64{-32600, -32602, -32600}
	for _, want := range wantCodes {
		var response map[string]any
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response["error"].(map[string]any)["code"] != want {
			t.Fatalf("unexpected MCP error: %#v", response)
		}
	}
	var ping map[string]any
	if err := decoder.Decode(&ping); err != nil {
		t.Fatal(err)
	}
	if ping["id"] != float64(3) || ping["result"] == nil {
		t.Fatalf("MCP did not recover after oversized request: %#v", ping)
	}
}

func TestMCPToolCall(t *testing.T) {
	request := map[string]any{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": map[string]any{"name": "status", "arguments": map[string]any{"root": t.TempDir()}}}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := MCPStdio(bytes.NewReader(append(raw, '\n')), &output); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	result, ok := response["result"].(map[string]any)
	if !ok || result["structuredContent"] == nil {
		t.Fatalf("unexpected tool response: %#v", response)
	}
}

func TestMCPNotificationHasNoResponse(t *testing.T) {
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"method\":\"ping\"}\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":9,\"method\":\"ping\"}\n")
	var output bytes.Buffer
	if err := MCPStdio(input, &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var response map[string]any
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response["id"] != float64(9) {
		t.Fatalf("unexpected response: %#v", response)
	}
	if decoder.More() {
		t.Fatal("notification unexpectedly produced a response")
	}
}

func TestDaemonHTTPBoundaryChecks(t *testing.T) {
	d := &daemon{root: t.TempDir(), role: "checkpoint", requests: map[string][]time.Time{}}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()
	d.handle(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}

	request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "text/plain")
	recorder = httptest.NewRecorder()
	d.handle(recorder, request)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("content type status = %d, want %d", recorder.Code, http.StatusUnsupportedMediaType)
	}

	request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", (1<<20)+1)))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	d.handle(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
	}

	secret := strings.Repeat("s", 32)
	t.Setenv("CODE_RELAY_WEBHOOK_SECRET", secret)
	body := []byte(`{"event":"push"}`)
	request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	d.handle(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing signature status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Hub-Signature-256", "sha256="+fmt.Sprintf("%x", mac.Sum(nil)))
	recorder = httptest.NewRecorder()
	d.handle(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("valid signature status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
}

func TestDaemonAddressRequiresAuthenticationOffHost(t *testing.T) {
	t.Setenv("CODE_RELAY_WEBHOOK_SECRET", "")
	if err := validateDaemonAddress("0.0.0.0:8765"); err == nil {
		t.Fatal("expected non-loopback daemon address to require a webhook secret")
	}
	t.Setenv("CODE_RELAY_WEBHOOK_SECRET", "short")
	if err := validateDaemonAddress("0.0.0.0:8765"); err == nil {
		t.Fatal("expected short webhook secret to be rejected")
	}
	t.Setenv("CODE_RELAY_WEBHOOK_SECRET", strings.Repeat("s", 32))
	if err := validateDaemonAddress("0.0.0.0:8765"); err != nil {
		t.Fatal(err)
	}
	if err := validateDaemonAddress("127.0.0.1:8765"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentAtomicWritesRemainValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "value.json")
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func(value int) {
			defer group.Done()
			if err := atomicJSON(path, map[string]any{"value": value}); err != nil {
				t.Errorf("atomic write: %v", err)
			}
		}(i)
	}
	group.Wait()
	var value map[string]any
	if err := readJSON(path, &value); err != nil {
		t.Fatal(err)
	}
	if value["value"] == nil {
		t.Fatalf("unexpected final value: %#v", value)
	}
}

func TestSymlinkParentIsRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not consistently available on Windows")
	}
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(realDirectory, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := pathWithinRoot(root, filepath.Join(link, "value.json")); err == nil {
		t.Fatal("expected project path symlink rejection")
	}
	if err := atomicJSON(filepath.Join(link, "value.json"), map[string]any{"ok": true}); err == nil {
		t.Fatal("expected symlink parent rejection")
	}
}

func TestPublishRunFetchAnalyzeAndReprocessInvalidReceipt(t *testing.T) {
	root := initTestRepository(t)
	commit := testGit(t, root, "rev-parse", "HEAD")
	raw := strings.Replace(testRunbook, "abc1234", commit, 1)
	if _, err := PublishRunbook(root, raw, false, true); err != nil {
		t.Fatal(err)
	}
	assertPendingPassed(t, root)
	receipt, err := FetchReceipt(root, "go-test")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "passed" || receipt.SourceCommit != commit {
		t.Fatalf("unexpected receipt: %#v", receipt)
	}
	analysis, err := Analyze(root, "go-test")
	if err != nil {
		t.Fatal(err)
	}
	if analysis["conclusion"] != "done" {
		t.Fatalf("unexpected analysis: %#v", analysis)
	}
	receiptPath := filepath.Join(root, "receipts", "go-test", "receipt.json")
	if err := os.WriteFile(receiptPath, []byte("{invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	assertPendingPassed(t, root)
	if _, err := FetchReceipt(root, "go-test"); err != nil {
		t.Fatalf("invalid receipt was not replaced: %v", err)
	}
}

func TestPublishRunbookDoesNotCreateEmptyCommit(t *testing.T) {
	root := initTestRepository(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v (%s)", err, out)
	}
	testGit(t, root, "remote", "add", "origin", remote)
	testGit(t, root, "push", "--set-upstream", "origin", "HEAD")
	commit := testGit(t, root, "rev-parse", "HEAD")
	raw := strings.Replace(testRunbook, "abc1234", commit, 1)
	if _, err := PublishRunbook(root, raw, false, false); err != nil {
		t.Fatal(err)
	}
	first := testGit(t, root, "rev-parse", "HEAD")
	if _, err := PublishRunbook(root, raw, false, false); err != nil {
		t.Fatal(err)
	}
	second := testGit(t, root, "rev-parse", "HEAD")
	if first != second {
		t.Fatalf("identical runbook publication created a new commit: %s -> %s", first, second)
	}
}

func assertPendingPassed(t *testing.T, root string) {
	t.Helper()
	results, err := RunPending(root, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0]["status"] != "passed" {
		t.Fatalf("unexpected pending result: %#v", results)
	}
}

func initTestRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	testGit(t, root, "init")
	testGit(t, root, "config", "user.name", "Code Relay Test")
	testGit(t, root, "config", "user.email", "relay@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "add", "README.md")
	testGit(t, root, "commit", "-m", "fixture")
	return root
}

func testGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := append([]string{"-C", root}, args...)
	out, err := exec.Command("git", command...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestRunPendingRecordsWorktreeFailure(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	path := filepath.Join(root, "runbooks", "missing-commit", "runbook.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	raw := strings.ReplaceAll(testRunbook, "go-test", "missing-commit")
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	results, err := RunPending(root, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0]["status"] != "blocked" {
		t.Fatalf("unexpected pending result: %#v", results)
	}
	receipt, err := FetchReceipt(root, "missing-commit")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "blocked" || len(receipt.Checks) != 1 || receipt.Checks[0].Name != "隔离 worktree" {
		t.Fatalf("unexpected receipt: %#v", receipt)
	}
}

func FuzzParseRunbook(f *testing.F) {
	f.Add(testRunbook)
	f.Add("# Runbook\n- runbook_id: x\n")
	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = parseRunbook(raw)
	})
}

func FuzzParseCommand(f *testing.F) {
	f.Add("go version")
	f.Add("echo 'quoted value'")
	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = parseCommand(raw)
	})
}
