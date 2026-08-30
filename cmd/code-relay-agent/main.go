package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zarcherlot/code-relay/internal/relay"
)

var version = "1.0.0"

func main() {
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
	case "validate-task":
		if len(args) == 0 {
			err = fmt.Errorf("缺少 task.md 路径")
		} else {
			err = relay.ValidateTaskFile(args[0])
		}
	case "run-task":
		fs := flag.NewFlagSet("run-task", flag.ContinueOnError)
		timeout := fs.Int("timeout", 600, "验证超时秒数")
		worktree := fs.String("worktree", "", "验证工作目录")
		_ = fs.Parse(args)
		if fs.NArg() == 0 {
			err = fmt.Errorf("缺少 task ID")
		} else {
			var receipt relay.Receipt
			receipt, err = relay.RunTask(root, fs.Arg(0), *timeout, *worktree)
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
	case "watcher":
		fs := flag.NewFlagSet("watcher", flag.ContinueOnError)
		interval := fs.Float64("poll-interval", 5, "轮询间隔")
		_ = fs.Parse(args)
		err = relay.Watch(root, *interval)
	case "daemon":
		fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
		role := fs.String("role", "verifier", "orchestrator 或 verifier")
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
	fmt.Println("Code Relay agent: status | doctor | validate-task | run-task | watcher | daemon")
}
