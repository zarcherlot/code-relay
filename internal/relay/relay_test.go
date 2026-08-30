package relay

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const testTask = `# Task
- task_id: go-test
- source_commit: abc1234
- target: B
- objective: smoke

## Validation Plan
1. go version

## Expected Results
- exits successfully
`

func TestParseTaskAndRun(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tasks", "go-test", "task.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(testTask), 0600); err != nil {
		t.Fatal(err)
	}
	receipt, err := RunTask(root, "go-test", 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "passed" || receipt.TaskID != "go-test" {
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

func TestInvalidTaskProducesBlockedReceipt(t *testing.T) {
	r := t.TempDir()
	p := filepath.Join(r, "tasks", "broken", "task.md")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("# Task\n- task_id: broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	receipt, err := RunTask(r, "broken", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "blocked" || len(receipt.Checks) != 1 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestTaskLockExcludesConcurrentExecution(t *testing.T) {
	root := t.TempDir()
	first, err := acquireTaskLock(root, "same-task", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()
	if _, err := acquireTaskLock(root, "same-task", 50*time.Millisecond); err == nil {
		t.Fatal("expected busy task lock")
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
	if _, err := BindProject(root, "orchestrator", "refs/heads/main"); err != nil {
		t.Fatal(err)
	}
	invite, err := CreateInvite(root, 30, true)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := DecodeInvite(invite["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if payload["repository"] != "https://github.com/example/relay" || payload["ref"] != "refs/heads/main" {
		t.Fatalf("unexpected invite: %#v", payload)
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
			_, joinErr := JoinVerifier(root, invite["url"].(string))
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
	markdown := testTask
	result, err := PublishTask(root, markdown, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if result["task_id"] != "go-test" {
		t.Fatalf("unexpected publish result: %#v", result)
	}
	if len(mcpTools()) < 9 {
		t.Fatalf("expected MCP tools, got %d", len(mcpTools()))
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

func FuzzParseTask(f *testing.F) {
	f.Add(testTask)
	f.Add("# Task\n- task_id: x\n")
	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = parseTask(raw)
	})
}
