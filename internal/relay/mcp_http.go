package relay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultMCPMaxBody       int64 = 1 << 20
	defaultMCPRatePerMinute       = 60
	defaultMCPMaxConcurrent       = 4
)

// MCPHTTPConfig configures the HTTP MCP adapter. Local mode pins calls to Root;
// hosted mode uses OAuthSession plus RemoteBackend and never accepts local
// filesystem paths through the MCP payload.
type MCPHTTPConfig struct {
	Root            string
	BearerToken     string
	OAuth           *OAuthService
	RemoteBackend   RemoteMCPBackend
	AuditLogger     *slog.Logger
	DomainChallenge string
	MaxBodyBytes    int64
	RatePerMinute   int
	MaxConcurrent   int
	AllowedTools    map[string]bool
	RequestTimeout  time.Duration
}

// MCPHTTPHandler returns an authenticated HTTP handler for the MCP endpoint.
// It is intentionally separate from MCPStdio so the desktop plugin contract
// remains unchanged while a public deployment can use HTTPS and a reverse
// proxy or managed container platform.
func MCPHTTPHandler(config MCPHTTPConfig) (http.Handler, error) {
	if config.RemoteBackend != nil {
		if config.OAuth == nil {
			return nil, errors.New("remote MCP requires OAuth authentication")
		}
	} else {
		if strings.TrimSpace(config.Root) == "" {
			return nil, errors.New("MCP root is required")
		}
		if strings.TrimSpace(config.BearerToken) == "" || len(config.BearerToken) < 32 {
			return nil, errors.New("MCP bearer token must contain at least 32 characters")
		}
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = defaultMCPMaxBody
	}
	if config.RatePerMinute <= 0 {
		config.RatePerMinute = defaultMCPRatePerMinute
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = defaultMCPMaxConcurrent
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 60 * time.Second
	}
	if config.AllowedTools == nil {
		config.AllowedTools = defaultRemoteMCPTools()
	}
	if config.AuditLogger == nil {
		config.AuditLogger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	return &mcpHTTPHandler{
		config:    config,
		clients:   make(map[string][]time.Time),
		semaphore: make(chan struct{}, config.MaxConcurrent),
	}, nil
}

func defaultRemoteMCPTools() map[string]bool {
	tools := make(map[string]bool, len(remoteToolNames))
	for name := range remoteToolNames {
		tools[name] = true
	}
	return tools
}

var remoteToolNames = map[string]struct{}{
	"bind_project":  {},
	"doctor":        {},
	"publish_task":  {},
	"status":        {},
	"fetch_receipt": {},
	"analyze":       {},
}

type mcpHTTPHandler struct {
	config    MCPHTTPConfig
	mu        sync.Mutex
	clients   map[string][]time.Time
	semaphore chan struct{}
}

func (h *mcpHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.config.OAuth != nil && h.config.OAuth.ServeHTTP(w, r) {
		return
	}
	if r.URL.Path == "/.well-known/openai-apps-challenge" {
		h.challenge(w, r)
		return
	}
	if r.URL.Path == "/healthz" {
		h.health(w, r)
		return
	}
	if r.URL.Path == "/" {
		h.home(w, r)
		return
	}
	if r.URL.Path != "/mcp" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var session OAuthSession
	clientID := clientAddress(r)
	if h.config.OAuth != nil {
		var err error
		session, err = h.config.OAuth.Authenticate(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="code-relay"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			h.config.AuditLogger.Warn("mcp authentication failed", "path", r.URL.Path, "remote", clientAddress(r))
			return
		}
		clientID = session.Subject
	} else if !h.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !h.allowClient(clientID) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	contentType := strings.ToLower(strings.Split(r.Header.Get("Content-Type"), ";")[0])
	if contentType != "application/json" {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if r.ContentLength > h.config.MaxBodyBytes {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, h.config.MaxBodyBytes+1))
	if err != nil {
		http.Error(w, "failed to read request", http.StatusBadRequest)
		return
	}
	if int64(len(body)) > h.config.MaxBodyBytes {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	var request mcpRequest
	if err := json.Unmarshal(body, &request); err != nil {
		h.writeMCP(w, mcpError(nil, -32700, "invalid JSON: "+err.Error()))
		return
	}
	if request.Method == "tools/call" {
		name, _ := request.Params["name"].(string)
		if !h.config.AllowedTools[name] {
			h.writeMCP(w, mcpError(request.ID, -32602, "tool is not enabled for the remote gateway"))
			return
		}
		args, ok := request.Params["arguments"].(map[string]any)
		if !ok {
			h.writeMCP(w, mcpError(request.ID, -32602, "tools/call requires object arguments"))
			return
		}
		if h.config.RemoteBackend == nil {
			// Never allow a remote caller to select a local path on the gateway.
			args["root"] = h.config.Root
			request.Params["arguments"] = args
		} else {
			// Hosted mode is repository-scoped and never accepts filesystem paths.
			delete(args, "root")
			request.Params["arguments"] = args
		}
	}
	if request.Method == "tools/list" {
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"tools": h.filteredTools()}}
		h.writeMCP(w, response)
		return
	}
	select {
	case h.semaphore <- struct{}{}:
		defer func() { <-h.semaphore }()
	case <-time.After(h.config.RequestTimeout):
		h.writeMCP(w, mcpError(request.ID, -32002, "MCP gateway concurrency limit reached"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.config.RequestTimeout)
	defer cancel()
	started := time.Now()
	var response map[string]any
	if h.config.RemoteBackend != nil && request.Method == "tools/call" {
		response = handleRemoteMCP(ctx, h.config.RemoteBackend, session, request)
	} else {
		response = handleMCP(request)
	}
	h.config.AuditLogger.Info("mcp tool request", "subject", clientID, "login", session.Login, "method", request.Method, "tool", requestToolName(request), "ok", response != nil && response["error"] == nil, "duration_ms", time.Since(started).Milliseconds())
	if response != nil && (request.HasID || request.JSONRPC != "2.0" || request.Method == "") {
		h.writeMCP(w, response)
	}
}

func (h *mcpHTTPHandler) challenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || strings.TrimSpace(h.config.DomainChallenge) == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, h.config.DomainChallenge)
}

func (h *mcpHTTPHandler) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "service": "code-relay-mcp", "version": versionString})
}

func (h *mcpHTTPHandler) home(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, "Code Relay MCP gateway is running.\nUse /auth/github to sign in or /healthz to check status.\n")
}

func (h *mcpHTTPHandler) authorized(r *http.Request) bool {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) < len("Bearer ") || !strings.EqualFold(value[:len("Bearer ")], "Bearer ") {
		return false
	}
	return value[len("Bearer "):] == h.config.BearerToken
}

func (h *mcpHTTPHandler) allowClient(client string) bool {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	recent := h.clients[client][:0]
	for _, stamp := range h.clients[client] {
		if now.Sub(stamp) < time.Minute {
			recent = append(recent, stamp)
		}
	}
	if len(recent) >= h.config.RatePerMinute {
		h.clients[client] = recent
		return false
	}
	h.clients[client] = append(recent, now)
	return true
}

func (h *mcpHTTPHandler) filteredTools() []map[string]any {
	tools := mcpTools()
	if h.config.RemoteBackend != nil {
		tools = remoteMCPToolSchemas(tools)
	}
	filtered := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if h.config.AllowedTools[name] {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func remoteMCPToolSchemas(tools []map[string]any) []map[string]any {
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if _, ok := remoteToolNames[name]; ok {
			if schema, ok := tool["inputSchema"].(map[string]any); ok {
				if props, ok := schema["properties"].(map[string]any); ok {
					delete(props, "root")
					props["repository"] = map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$"}
					props["ref"] = map[string]any{"type": "string", "pattern": "^refs/heads/[A-Za-z0-9._/-]+$"}
				}
				required, _ := schema["required"].([]string)
				if required == nil {
					required = []string{}
				}
				if !containsString(required, "repository") {
					required = append(required, "repository")
				}
				if !containsString(required, "ref") {
					required = append(required, "ref")
				}
				schema["required"] = required
			}
		}
	}
	return tools
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func requestToolName(request mcpRequest) string {
	if request.Method != "tools/call" {
		return ""
	}
	name, _ := request.Params["name"].(string)
	return name
}

func handleRemoteMCP(ctx context.Context, backend RemoteMCPBackend, session OAuthSession, request mcpRequest) map[string]any {
	if request.JSONRPC != "2.0" || request.Method == "" {
		return mcpError(request.ID, -32600, "invalid JSON-RPC request")
	}
	if request.Method == "notifications/initialized" {
		return nil
	}
	if request.Method == "initialize" || request.Method == "ping" {
		return handleMCP(request)
	}
	if request.Method != "tools/call" {
		return handleMCP(request)
	}
	name, nameOK := request.Params["name"].(string)
	args, argsOK := request.Params["arguments"].(map[string]any)
	if !nameOK || name == "" || !argsOK {
		return mcpError(request.ID, -32602, "tools/call requires string name and object arguments")
	}
	value, err := backend.Call(ctx, session, name, args)
	if err != nil {
		return map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32000, "message": err.Error()}}
	}
	encoded, _ := json.MarshalIndent(value, "", "  ")
	return map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"content": []map[string]any{{"type": "text", "text": string(encoded)}}, "structuredContent": value}}
}

func (h *mcpHTTPHandler) writeMCP(w http.ResponseWriter, value map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("MCP-Protocol-Version", "2024-11-05")
	_ = json.NewEncoder(w).Encode(value)
}

func clientAddress(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
