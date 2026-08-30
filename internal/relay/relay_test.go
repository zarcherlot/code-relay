package relay

import (
	"os"
	"path/filepath"
	"testing"
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
