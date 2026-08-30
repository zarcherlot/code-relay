package relay

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type mcpRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

func mcpTools() []map[string]any {
	tool := func(name, description string, properties map[string]any, required []string) map[string]any {
		if required == nil {
			required = []string{}
		}
		return map[string]any{"name": name, "description": description, "inputSchema": map[string]any{"type": "object", "properties": properties, "required": required}}
	}
	root := map[string]any{"type": "string"}
	return []map[string]any{
		tool("bind_project", "Bind the current project and branch to Code Relay.", map[string]any{"root": root, "role": map[string]any{"type": "string", "enum": []string{"orchestrator", "verifier"}}, "ref": map[string]any{"type": "string"}}, []string{"role"}),
		tool("create_verifier_invite", "Create a short-lived verifier join link.", map[string]any{"root": root, "expires": map[string]any{"type": "integer", "minimum": 5, "maximum": 1440}, "one_time": map[string]any{"type": "boolean"}}, nil),
		tool("join_verifier", "Join a branch-scoped verifier subscription.", map[string]any{"root": root, "url": map[string]any{"type": "string"}, "destination": map[string]any{"type": "string"}}, []string{"url"}),
		tool("watcher_status", "Show the legacy local watcher state.", map[string]any{"root": root}, nil),
		tool("stop_watcher", "Stop the legacy local watcher state.", map[string]any{"root": root}, nil),
		tool("doctor", "Run non-mutating local health checks.", map[string]any{"root": root}, nil),
		tool("publish_task", "Validate and publish a task.md.", map[string]any{"root": root, "markdown": map[string]any{"type": "string"}, "force": map[string]any{"type": "boolean"}, "no_git": map[string]any{"type": "boolean"}}, []string{"markdown"}),
		tool("status", "Show task and receipt status.", map[string]any{"root": root}, nil),
		tool("fetch_receipt", "Load and validate a task receipt.", map[string]any{"root": root, "task_id": map[string]any{"type": "string"}}, []string{"task_id"}),
		tool("analyze", "Analyze a task receipt and determine the next state.", map[string]any{"root": root, "task_id": map[string]any{"type": "string"}}, []string{"task_id"}),
	}
}

func MCPStdio(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 2*maxTask)
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var request mcpRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": err.Error()}})
			continue
		}
		response := handleMCP(request)
		if response != nil {
			if err := encoder.Encode(response); err != nil {
				return err
			}
			if flusher, ok := out.(interface{ Flush() error }); ok {
				_ = flusher.Flush()
			}
		}
	}
	return scanner.Err()
}

func handleMCP(request mcpRequest) map[string]any {
	if request.Method == "notifications/initialized" {
		return nil
	}
	response := func(result any) map[string]any {
		return map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}
	}
	errResponse := func(err error) map[string]any {
		return map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32000, "message": err.Error()}}
	}
	switch request.Method {
	case "initialize":
		return response(map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "code-relay", "version": versionString}})
	case "ping":
		return response(map[string]any{})
	case "tools/list":
		return response(map[string]any{"tools": mcpTools()})
	case "tools/call":
		name, _ := request.Params["name"].(string)
		args, _ := request.Params["arguments"].(map[string]any)
		value, err := callMCPTool(name, args)
		if err != nil {
			return errResponse(err)
		}
		encoded, _ := json.MarshalIndent(value, "", "  ")
		return response(map[string]any{"content": []map[string]any{{"type": "text", "text": string(encoded)}}, "structuredContent": value})
	default:
		return map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32601, "message": fmt.Sprintf("method not found: %s", request.Method)}}
	}
}

var versionString = "1.0.0"

func SetVersion(value string) {
	if value != "" {
		versionString = value
	}
}

func callMCPTool(name string, args map[string]any) (any, error) {
	root, _ := args["root"].(string)
	if root == "" {
		root = "."
	}
	switch name {
	case "bind_project":
		role, _ := args["role"].(string)
		ref, _ := args["ref"].(string)
		value, err := BindProject(root, role, ref)
		if err != nil {
			return nil, err
		}
		if role == "orchestrator" {
			invite, inviteErr := CreateInvite(root, intNumber(args["expires"], 30), boolDefault(args["one_time"], true))
			if inviteErr != nil {
				return nil, inviteErr
			}
			value["invite"] = invite
		}
		return value, nil
	case "create_verifier_invite":
		return CreateInvite(root, intNumber(args["expires"], 30), boolDefault(args["one_time"], true))
	case "join_verifier":
		url, _ := args["url"].(string)
		if destination, ok := args["destination"].(string); ok && destination != "" {
			return CloneAndJoin(root, url, destination)
		}
		if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
			return nil, fmt.Errorf("当前目录不是 Git 工程；请提供 destination 以克隆邀请中的仓库")
		}
		return JoinVerifier(root, url)
	case "watcher_status":
		return WatcherStatus(root), nil
	case "stop_watcher":
		return StopWatcher(root), nil
	case "doctor":
		return Doctor(root)
	case "publish_task":
		markdown, _ := args["markdown"].(string)
		return PublishTask(root, markdown, boolDefault(args["force"], false), boolDefault(args["no_git"], true))
	case "status":
		return Status(root)
	case "fetch_receipt":
		id, _ := args["task_id"].(string)
		return FetchReceipt(root, id)
	case "analyze":
		id, _ := args["task_id"].(string)
		return Analyze(root, id)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func intNumber(value any, fallback int) int {
	switch n := value.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return fallback
	}
}

func boolDefault(value any, fallback bool) bool {
	if v, ok := value.(bool); ok {
		return v
	}
	return fallback
}
