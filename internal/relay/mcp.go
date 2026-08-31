package relay

import (
	"bufio"
	"encoding/json"
	"errors"
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
	HasID   bool           `json:"-"`
}

func (request *mcpRequest) UnmarshalJSON(data []byte) error {
	type requestAlias mcpRequest
	var decoded requestAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*request = mcpRequest(decoded)
	_, request.HasID = fields["id"]
	return nil
}

func mcpTools() []map[string]any {
	tool := func(name, description string, properties map[string]any, required []string, readOnly, openWorld, destructive bool) map[string]any {
		if required == nil {
			required = []string{}
		}
		return map[string]any{
			"name":        name,
			"description": description,
			"inputSchema": map[string]any{"type": "object", "properties": properties, "required": required},
			"annotations": map[string]any{
				"readOnlyHint":    readOnly,
				"openWorldHint":   openWorld,
				"destructiveHint": destructive,
			},
		}
	}
	root := map[string]any{"type": "string"}
	return []map[string]any{
		tool("bind_project", "Bind the current project and branch to Code Relay.", map[string]any{"root": root, "role": map[string]any{"type": "string", "enum": []string{"orchestrator", "checkpoint"}}, "ref": map[string]any{"type": "string"}}, []string{"role"}, false, true, true),
		tool("create_checkpoint_invite", "Create a short-lived checkpoint join link.", map[string]any{"root": root, "expires": map[string]any{"type": "integer", "minimum": 5, "maximum": 1440}, "one_time": map[string]any{"type": "boolean"}}, nil, false, true, false),
		tool("join_checkpoint", "Join a branch-scoped checkpoint subscription.", map[string]any{"root": root, "url": map[string]any{"type": "string"}, "destination": map[string]any{"type": "string"}}, []string{"url"}, false, true, true),
		tool("watcher_status", "Show the legacy local watcher state.", map[string]any{"root": root}, nil, true, false, false),
		tool("stop_watcher", "Stop the legacy local watcher state.", map[string]any{"root": root}, nil, false, false, true),
		tool("doctor", "Run non-mutating local health checks.", map[string]any{"root": root}, nil, true, false, false),
		tool("publish_runbook", "Validate and publish a runbook.md.", map[string]any{"root": root, "markdown": map[string]any{"type": "string"}, "force": map[string]any{"type": "boolean"}, "no_git": map[string]any{"type": "boolean"}}, []string{"markdown"}, false, true, true),
		tool("status", "Show runbook and receipt status.", map[string]any{"root": root}, nil, true, false, false),
		tool("fetch_receipt", "Load and validate a runbook receipt.", map[string]any{"root": root, "runbook_id": map[string]any{"type": "string"}}, []string{"runbook_id"}, true, false, false),
		tool("analyze", "Analyze a runbook receipt and determine the next state.", map[string]any{"root": root, "runbook_id": map[string]any{"type": "string"}}, []string{"runbook_id"}, true, false, false),
	}
}

func MCPStdio(in io.Reader, out io.Writer) error {
	reader := bufio.NewReaderSize(in, 64*1024)
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	for {
		line, oversized, err := readMCPLine(reader, 2*maxRunbook)
		if errors.Is(err, io.EOF) && len(line) == 0 && !oversized {
			return nil
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if oversized {
			_ = encoder.Encode(mcpError(nil, -32600, "request exceeds maximum size"))
			if errors.Is(err, io.EOF) {
				return nil
			}
			continue
		}
		if len(line) == 0 {
			if errors.Is(err, io.EOF) {
				return nil
			}
			continue
		}
		var request mcpRequest
		if decodeErr := json.Unmarshal(line, &request); decodeErr != nil {
			_ = encoder.Encode(mcpError(nil, -32700, decodeErr.Error()))
			continue
		}
		response := handleMCP(request)
		if response != nil && (request.HasID || request.JSONRPC != "2.0" || request.Method == "") {
			if err := encoder.Encode(response); err != nil {
				return err
			}
			if flusher, ok := out.(interface{ Flush() error }); ok {
				_ = flusher.Flush()
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

func handleMCP(request mcpRequest) map[string]any {
	if request.JSONRPC != "2.0" || request.Method == "" {
		return mcpError(request.ID, -32600, "invalid JSON-RPC request")
	}
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
		name, nameOK := request.Params["name"].(string)
		args, argsOK := request.Params["arguments"].(map[string]any)
		if !nameOK || name == "" || !argsOK {
			return mcpError(request.ID, -32602, "tools/call requires string name and object arguments")
		}
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

func mcpError(id any, code int, message string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}}
}

func readMCPLine(reader *bufio.Reader, limit int) ([]byte, bool, error) {
	line := make([]byte, 0, min(limit, 64*1024))
	oversized := false
	for {
		part, err := reader.ReadSlice('\n')
		if !oversized && len(line)+len(part) <= limit {
			line = append(line, part...)
		} else if len(part) > 0 {
			oversized = true
		}
		if err == nil {
			return line, oversized, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, oversized, err
	}
}

var versionString = "3.0.0"

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
	case "create_checkpoint_invite":
		return CreateInvite(root, intNumber(args["expires"], 30), boolDefault(args["one_time"], true))
	case "join_checkpoint":
		url, _ := args["url"].(string)
		if destination, ok := args["destination"].(string); ok && destination != "" {
			return CloneAndJoin(root, url, destination)
		}
		if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
			return nil, fmt.Errorf("当前目录不是 Git 工程；请提供 destination 以克隆邀请中的仓库")
		}
		return JoinCheckpoint(root, url)
	case "watcher_status":
		return WatcherStatus(root), nil
	case "stop_watcher":
		return StopWatcher(root), nil
	case "doctor":
		return Doctor(root)
	case "publish_runbook":
		markdown, _ := args["markdown"].(string)
		return PublishRunbook(root, markdown, boolDefault(args["force"], false), boolDefault(args["no_git"], true))
	case "status":
		return Status(root)
	case "fetch_receipt":
		id, _ := args["runbook_id"].(string)
		return FetchReceipt(root, id)
	case "analyze":
		id, _ := args["runbook_id"].(string)
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
