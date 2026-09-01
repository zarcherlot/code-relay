package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zarcherlot/code-relay/internal/relay"
)

var version = "3.1.0"

func main() {
	relay.SetVersion(version)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	root := "."
	args := os.Args[2:]
	parsedArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--root" && i+1 < len(args) {
			root, i = args[i+1], i+1
			continue
		}
		parsedArgs = append(parsedArgs, args[i])
	}
	args = parsedArgs
	root, _ = filepath.Abs(root)
	var err error
	switch os.Args[1] {
	case "--version", "version":
		fmt.Println("code-relay-agent " + version)
		return
	case "mcp-stdio":
		if err := relay.MCPStdio(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	case "status":
		var value any
		value, err = relay.Status(root)
		if err == nil {
			printJSON(value)
		}
	case "doctor":
		var value map[string]any
		value, err = relay.Doctor(root)
		if err == nil {
			printJSON(value)
			if value["status"] == "error" {
				os.Exit(1)
			}
		}
	case "validate-runbook":
		if len(args) == 0 {
			err = fmt.Errorf("缺少 runbook.md 路径")
		} else {
			err = relay.ValidateRunbookFile(args[0])
		}
	case "run-runbook":
		fs := flag.NewFlagSet("run-runbook", flag.ContinueOnError)
		timeout := fs.Int("timeout", 600, "验证超时秒数")
		worktree := fs.String("worktree", "", "验证工作目录")
		_ = fs.Parse(args)
		if fs.NArg() == 0 {
			err = fmt.Errorf("缺少 runbook ID")
		} else {
			var receipt relay.Receipt
			receipt, err = relay.RunRunbook(root, fs.Arg(0), *timeout, *worktree)
			if err == nil {
				err = relay.PersistReceipt(root, receipt)
			}
			if err == nil {
				printJSON(receipt)
				if receipt.Status != "passed" {
					os.Exit(2)
				}
			}
		}
	case "bind-project", "project-bind":
		fs := flag.NewFlagSet("bind-project", flag.ContinueOnError)
		role := fs.String("role", "", "orchestrator 或 checkpoint")
		ref := fs.String("ref", "", "refs/heads/<branch>")
		_ = fs.Parse(args)
		var value map[string]any
		value, err = relay.BindProject(root, *role, *ref)
		if err == nil && *role == "orchestrator" {
			var invite map[string]any
			invite, err = relay.CreateInvite(root, 30, true)
			if err == nil {
				value["invite"] = invite
			}
		}
		if err == nil {
			printJSON(value)
		}
	case "invite":
		fs := flag.NewFlagSet("invite", flag.ContinueOnError)
		expires := fs.Int("expires", 30, "邀请有效期（分钟）")
		reusable := fs.Bool("reusable", false, "允许重复加入")
		_ = fs.Parse(args)
		var value map[string]any
		value, err = relay.CreateInvite(root, *expires, !*reusable)
		if err == nil {
			printJSON(value)
		}
	case "join":
		fs := flag.NewFlagSet("join", flag.ContinueOnError)
		destination := fs.String("destination", "", "空目录克隆目标")
		_ = fs.Parse(args)
		if fs.NArg() == 0 {
			err = fmt.Errorf("缺少邀请链接")
		} else {
			var value map[string]any
			if *destination != "" {
				value, err = relay.CloneAndJoin(root, fs.Arg(0), *destination)
			} else {
				value, err = relay.JoinCheckpoint(root, fs.Arg(0))
			}
			if err == nil {
				printJSON(value)
			}
		}
	case "publish":
		fs := flag.NewFlagSet("publish", flag.ContinueOnError)
		file := fs.String("file", "", "runbook.md 路径")
		runbookID := fs.String("runbook-id", "", "runbook ID")
		source := fs.String("source-commit", "", "源 commit SHA")
		target := fs.String("target", "B", "验证目标")
		objective := fs.String("objective", "", "runbook 目标")
		validation := multiStringFlag{}
		expected := multiStringFlag{}
		fs.Var(&validation, "validation", "验证命令（可重复）")
		fs.Var(&expected, "expected", "预期结果（可重复）")
		force := fs.Bool("force", false, "允许覆盖相同 runbook ID")
		noGit := fs.Bool("no-git", false, "不执行 git add/commit/push")
		_ = fs.Parse(args)
		var markdown string
		if *file != "" {
			data, readErr := os.ReadFile(*file)
			if readErr != nil {
				err = readErr
			} else {
				markdown = string(data)
			}
		} else if *runbookID == "" || *source == "" || *objective == "" || len(validation) == 0 {
			err = fmt.Errorf("publish 需要 --file，或同时提供 --runbook-id/--source-commit/--objective/--validation")
		} else {
			if len(expected) == 0 {
				expected = append(expected, "验证命令全部成功")
			}
			var b strings.Builder
			fmt.Fprintf(&b, "# Runbook\n- runbook_id: %s\n- source_commit: %s\n- target: %s\n- objective: %s\n\n## Validation Plan\n", *runbookID, *source, *target, *objective)
			for i, item := range validation {
				fmt.Fprintf(&b, "%d. %s\n", i+1, item)
			}
			b.WriteString("\n## Expected Results\n")
			for _, item := range expected {
				fmt.Fprintf(&b, "- %s\n", item)
			}
			b.WriteString("\n## Receipt Contract\n- 执行状态、实际命令和环境、每项验证的 expected / actual / status。\n")
			markdown = b.String()
		}
		if err == nil {
			var value map[string]any
			value, err = relay.PublishRunbook(root, markdown, *force, *noGit)
			if err == nil {
				printJSON(value)
			}
		}
	case "fetch-receipt":
		if len(args) == 0 {
			err = fmt.Errorf("缺少 runbook ID")
		} else {
			var value relay.Receipt
			value, err = relay.FetchReceipt(root, args[0])
			if err == nil {
				printJSON(value)
			}
		}
	case "analyze":
		if len(args) == 0 {
			err = fmt.Errorf("缺少 runbook ID")
		} else {
			var value map[string]any
			value, err = relay.Analyze(root, args[0])
			if err == nil {
				printJSON(value)
			}
		}
	case "run-pending":
		fs := flag.NewFlagSet("run-pending", flag.ContinueOnError)
		timeout := fs.Int("timeout", 600, "单份 runbook 验证超时秒数")
		_ = fs.Parse(args)
		var value []map[string]any
		value, err = relay.RunPending(root, *timeout)
		if err == nil {
			printJSON(value)
		}
	case "publish-receipts":
		err = relay.PublishReceipts(root)
	case "watcher":
		fs := flag.NewFlagSet("watcher", flag.ContinueOnError)
		interval := fs.Float64("poll-interval", 5, "轮询间隔")
		_ = fs.Parse(args)
		err = relay.Watch(root, *interval)
	case "daemon":
		fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
		role := fs.String("role", "checkpoint", "orchestrator 或 checkpoint")
		interval := fs.Float64("poll-interval", 5, "轮询间隔")
		addr := fs.String("addr", "127.0.0.1:8765", "Webhook 监听地址")
		_ = fs.Parse(args)
		err = relay.Daemon(root, *role, *interval, *addr)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func printJSON(value any) { data, _ := json.MarshalIndent(value, "", "  "); fmt.Println(string(data)) }
func usage() {
	fmt.Println("Code Relay agent: mcp-stdio | bind-project | invite | join | publish | status | fetch-receipt | analyze | validate-runbook | run-runbook | run-pending | publish-receipts | watcher | daemon | doctor")
}

type multiStringFlag []string

func (f *multiStringFlag) String() string { return strings.Join(*f, ",") }
func (f *multiStringFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}
