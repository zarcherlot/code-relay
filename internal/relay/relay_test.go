package relay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

func FuzzParseTask(f *testing.F) {
	f.Add(testTask)
	f.Add("# Task\n- task_id: x\n")
	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = parseTask(raw)
	})
}
