package relay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	Root              string
	BearerToken       string
	OAuth             *OAuthService
	RemoteBackend     RemoteMCPBackend
	AuditLogger       *slog.Logger
	DomainChallenge   string
	MaxBodyBytes      int64
	RatePerMinute     int
	MaxConcurrent     int
	AllowedTools      map[string]bool
	AllowedOrigins    []string
	RequestTimeout    time.Duration
	SessionTTL        time.Duration
	SSEHeartbeat      time.Duration
	SSEMaxQueue       int
	SSEEventHistory   int
	MaxSSEConnections int
	EventStore        SessionEventStore
	ControlPlane      ControlPlane
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
	if config.SessionTTL <= 0 {
		config.SessionTTL = 30 * time.Minute
	}
	if config.SSEHeartbeat <= 0 {
		config.SSEHeartbeat = 15 * time.Second
	}
	if config.SSEMaxQueue <= 0 {
		config.SSEMaxQueue = 32
	}
	if config.SSEEventHistory <= 0 {
		config.SSEEventHistory = 256
	}
	if config.MaxSSEConnections <= 0 {
		config.MaxSSEConnections = 1000
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
		sessions:  make(map[string]*mcpHTTPSession),
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
	"bind_project":    {},
	"doctor":          {},
	"publish_runbook": {},
	"status":          {},
	"fetch_receipt":   {},
	"analyze":         {},
}

type mcpHTTPHandler struct {
	config         MCPHTTPConfig
	mu             sync.Mutex
	clients        map[string][]time.Time
	sessions       map[string]*mcpHTTPSession
	sseConnections int
	semaphore      chan struct{}
	draining       atomic.Bool
}

type mcpSSEEvent struct {
	id   string
	data []byte
}

type mcpHTTPSession struct {
	mu          sync.Mutex
	id          string
	owner       string
	expiresAt   time.Time
	nextEventID uint64
	events      []mcpSSEEvent
	subscribers map[chan mcpSSEEvent]struct{}
	closed      bool
}

const (
	mcpProtocolVersion = "2024-11-05"
	mcpCurrentProtocol = "2025-06-18"
)

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
	if h.draining.Load() && r.Method != http.MethodDelete {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "MCP gateway is draining", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodOptions {
		if !validMCPOrigin(r, h.config.AllowedOrigins) {
			http.Error(w, "invalid origin", http.StatusForbidden)
			return
		}
		h.writeCORS(w, r)
		w.Header().Set("Allow", "GET, POST, DELETE, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var session OAuthSession
	clientID := clientAddress(r)
	if h.config.OAuth != nil {
		var err error
		session, err = h.config.OAuth.Authenticate(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="code-relay", resource_metadata="`+h.oauthResourceMetadataURL(r)+`"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			h.config.AuditLogger.Warn("mcp authentication failed", "path", r.URL.Path, "remote", clientAddress(r))
			return
		}
		clientID = session.Subject
	} else if !h.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	h.writeCORS(w, r)
	if !validMCPOrigin(r, h.config.AllowedOrigins) {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	if err := validateMCPProtocol(r); err != nil {
		w.Header().Set("MCP-Protocol-Version", mcpCurrentProtocol)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodGet {
		if !acceptsMCPEventStream(r.Header.Get("Accept")) {
			w.Header().Set("Accept", "text/event-stream")
			http.Error(w, "GET /mcp requires Accept: text/event-stream", http.StatusNotAcceptable)
			return
		}
		h.sessionSSE(w, r, r.Header.Get("Mcp-Session-Id"), r.Header.Get("Last-Event-ID"), clientID)
		return
	}
	if r.Method == http.MethodDelete {
		h.deleteSession(w, r, r.Header.Get("Mcp-Session-Id"), clientID)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
	mcpSessionID := strings.TrimSpace(r.Header.Get("Mcp-Session-Id"))
	if request.Method == "initialize" && mcpSessionID == "" {
		mcpSessionID = h.newSession(clientID)
	} else if mcpSessionID != "" {
		mcpSession := h.lookupSession(mcpSessionID)
		if mcpSession == nil && h.config.EventStore != nil {
			mcpSession = h.adoptSession(mcpSessionID, clientID)
		}
		if mcpSession == nil {
			http.Error(w, "unknown MCP session", http.StatusBadRequest)
			return
		}
		if mcpSession.owner != clientID {
			http.Error(w, "MCP session belongs to another client", http.StatusForbidden)
			return
		}
	}
	if mcpSessionID != "" {
		w.Header().Set("Mcp-Session-Id", mcpSessionID)
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
			if session.Repository != "" && stringArg(args, "repository") == "" {
				args["repository"] = session.Repository
			}
			if session.Ref != "" && stringArg(args, "ref") == "" {
				args["ref"] = session.Ref
			}
			request.Params["arguments"] = args
		}
	}
	if request.Method == "tools/list" {
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"tools": h.filteredTools()}}
		h.writeMCP(w, response)
		return
	}
	if mcpSessionID != "" && request.Method == "tools/call" {
		h.publishSessionEvent(mcpSessionID, map[string]any{"jsonrpc": "2.0", "method": "notifications/message", "params": map[string]any{"level": "info", "data": "tool execution started", "tool": requestToolName(request)}})
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
		if h.config.ControlPlane != nil {
			if err := h.config.ControlPlane.EnsureTenant(ctx, session.Subject, session.Login); err != nil {
				h.config.AuditLogger.Warn("control plane tenant check failed", "subject", session.Subject, "error", err)
				h.writeMCP(w, mcpError(request.ID, -32001, "control plane unavailable"))
				return
			}
		}
		response = handleRemoteMCP(ctx, h.config.RemoteBackend, session, request)
		h.persistBinding(w, session, request, response)
	} else {
		response = handleMCP(request)
	}
	h.config.AuditLogger.Info("mcp tool request", "subject", clientID, "login", session.Login, "method", request.Method, "tool", requestToolName(request), "ok", response != nil && response["error"] == nil, "duration_ms", time.Since(started).Milliseconds())
	if mcpSessionID != "" && request.Method == "tools/call" {
		status := "completed"
		if response == nil || response["error"] != nil {
			status = "failed"
		}
		h.publishSessionEvent(mcpSessionID, map[string]any{"jsonrpc": "2.0", "method": "notifications/message", "params": map[string]any{"level": "info", "data": "tool execution " + status, "tool": requestToolName(request)}})
	}
	if h.config.ControlPlane != nil && session.Subject != "" {
		metadata, _ := json.Marshal(map[string]any{"method": request.Method, "tool": requestToolName(request), "ok": response != nil && response["error"] == nil})
		if err := h.config.ControlPlane.AppendAudit(context.Background(), session.Subject, clientID, "mcp.request", "", "", metadata); err != nil {
			h.config.AuditLogger.Warn("control plane audit write failed", "subject", session.Subject, "error", err)
		}
	}
	if response != nil && (request.HasID || request.JSONRPC != "2.0" || request.Method == "") {
		if acceptsMCPEventStream(r.Header.Get("Accept")) {
			h.writeMCPSSE(w, response)
		} else {
			h.writeMCP(w, response)
		}
	} else if response == nil {
		w.WriteHeader(http.StatusAccepted)
	}
}

// SessionEventStore provides cross-instance ordered event delivery for the
// Streamable HTTP GET stream. Implementations must return events strictly
// after the supplied cursor and may block until the requested timeout.
type SessionEventStore interface {
	Publish(context.Context, string, []byte, int) (string, error)
	Read(context.Context, string, string, time.Duration) ([]SessionEvent, error)
}

// SessionEvent is the public event representation used by shared event-store
// implementations. IDs must be ordered cursors within a session.
type SessionEvent struct {
	ID   string
	Data []byte
}

// BeginDrain stops admitting new MCP requests while allowing active SSE
// streams and in-flight tool calls to finish during a rolling deployment.
func (h *mcpHTTPHandler) BeginDrain() {
	h.draining.Store(true)
}

// WaitForDrain waits until all active event streams have disconnected or the
// supplied context expires. It is safe to call more than once.
func (h *mcpHTTPHandler) WaitForDrain(ctx context.Context) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		h.mu.Lock()
		active := h.sseConnections
		h.mu.Unlock()
		if active == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (h *mcpHTTPHandler) oauthResourceMetadataURL(r *http.Request) string {
	if issuer := strings.TrimRight(h.config.OAuth.config.IssuerURL, "/"); issuer != "" {
		return issuer + "/.well-known/oauth-protected-resource"
	}
	scheme := "https"
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	return scheme + "://" + r.Host + "/.well-known/oauth-protected-resource"
}

func (h *mcpHTTPHandler) publishSessionEvent(id string, value map[string]any) {
	session := h.lookupSession(id)
	if session == nil {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	if h.config.EventStore != nil {
		if _, err := h.config.EventStore.Publish(context.Background(), id, data, h.config.SSEEventHistory); err != nil {
			h.config.AuditLogger.Warn("mcp event publish failed", "session_id", id, "error", err)
		}
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return
	}
	session.nextEventID++
	event := mcpSSEEvent{id: fmt.Sprintf("%016x", session.nextEventID), data: data}
	session.events = append(session.events, event)
	if len(session.events) > h.config.SSEEventHistory {
		session.events = session.events[len(session.events)-h.config.SSEEventHistory:]
	}
	for subscriber := range session.subscribers {
		select {
		case subscriber <- event:
		default:
			// Slow consumers are disconnected by closing their bounded queue.
			close(subscriber)
			delete(session.subscribers, subscriber)
		}
	}
}

func validMCPOrigin(r *http.Request, allowedOrigins []string) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	for _, allowed := range allowedOrigins {
		if strings.EqualFold(strings.TrimRight(strings.TrimSpace(allowed), "/"), strings.TrimRight(origin, "/")) {
			return true
		}
	}
	if len(allowedOrigins) > 0 {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func (h *mcpHTTPHandler) writeCORS(w http.ResponseWriter, r *http.Request) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" || !validMCPOrigin(r, h.config.AllowedOrigins) {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Method, Mcp-Name, Mcp-Protocol-Version, Mcp-Session-Id, Last-Event-ID")
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Protocol-Version, Mcp-Session-Id")
	w.Header().Add("Vary", "Origin")
}

func validateMCPProtocol(r *http.Request) error {
	version := strings.TrimSpace(r.Header.Get("MCP-Protocol-Version"))
	if version == "" || version == mcpProtocolVersion || version == mcpCurrentProtocol {
		return nil
	}
	return errors.New("unsupported MCP protocol version")
}

func acceptsMCPEventStream(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]), "text/event-stream") {
			return true
		}
	}
	return false
}

func newMCPSessionID() string {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func (h *mcpHTTPHandler) newSession(owner string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	for {
		id := newMCPSessionID()
		if _, exists := h.sessions[id]; !exists {
			h.sessions[id] = &mcpHTTPSession{id: id, owner: owner, expiresAt: time.Now().Add(h.config.SessionTTL), subscribers: make(map[chan mcpSSEEvent]struct{})}
			return id
		}
	}
}

func (h *mcpHTTPHandler) adoptSession(id, owner string) *mcpHTTPSession {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if session := h.sessions[id]; session != nil {
		return session
	}
	session := &mcpHTTPSession{id: id, owner: owner, expiresAt: time.Now().Add(h.config.SessionTTL), subscribers: make(map[chan mcpSSEEvent]struct{})}
	h.sessions[id] = session
	return session
}

func (h *mcpHTTPHandler) lookupSession(id string) *mcpHTTPSession {
	if id == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.sessions[id]
	if session == nil {
		return nil
	}
	if !session.expiresAt.IsZero() && time.Now().After(session.expiresAt) {
		delete(h.sessions, id)
		session.mu.Lock()
		session.closed = true
		for sub := range session.subscribers {
			close(sub)
		}
		session.subscribers = nil
		session.mu.Unlock()
		return nil
	}
	return session
}

func (h *mcpHTTPHandler) deleteSession(w http.ResponseWriter, r *http.Request, id, owner string) {
	if id == "" {
		http.Error(w, "Mcp-Session-Id is required", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	session := h.sessions[id]
	if session != nil && session.owner == owner {
		delete(h.sessions, id)
	}
	h.mu.Unlock()
	if session == nil {
		http.Error(w, "unknown MCP session", http.StatusNotFound)
		return
	}
	if session.owner != owner {
		http.Error(w, "MCP session belongs to another client", http.StatusForbidden)
		return
	}
	session.mu.Lock()
	session.closed = true
	for sub := range session.subscribers {
		close(sub)
	}
	session.subscribers = nil
	session.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (h *mcpHTTPHandler) sessionSSE(w http.ResponseWriter, r *http.Request, id, lastID, owner string) {
	if id == "" {
		http.Error(w, "Mcp-Session-Id is required", http.StatusBadRequest)
		return
	}
	session := h.lookupSession(id)
	if session == nil && h.config.EventStore != nil {
		session = h.adoptSession(id, owner)
	}
	if session == nil {
		http.Error(w, "unknown MCP session", http.StatusNotFound)
		return
	}
	if session.owner != owner {
		http.Error(w, "MCP session belongs to another client", http.StatusForbidden)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("MCP-Protocol-Version", mcpCurrentProtocol)
	if h.config.EventStore != nil {
		h.sessionSSEExternal(w, r, session, lastID, flusher)
		return
	}
	h.mu.Lock()
	if h.config.MaxSSEConnections > 0 && h.sseConnections >= h.config.MaxSSEConnections {
		h.mu.Unlock()
		http.Error(w, "SSE connection limit reached", http.StatusTooManyRequests)
		return
	}
	h.sseConnections++
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.sseConnections--
		h.mu.Unlock()
	}()
	sub := make(chan mcpSSEEvent, h.config.SSEMaxQueue)
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		http.Error(w, "MCP session is closed", http.StatusGone)
		return
	}
	for _, event := range session.events {
		if lastID == "" || event.id > lastID {
			writeSSEEvent(w, event)
		}
	}
	session.subscribers[sub] = struct{}{}
	session.mu.Unlock()
	flusher.Flush()
	defer func() {
		session.mu.Lock()
		delete(session.subscribers, sub)
		session.mu.Unlock()
	}()
	ticker := time.NewTicker(h.config.SSEHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-sub:
			if !open {
				return
			}
			writeSSEEvent(w, event)
			flusher.Flush()
		case <-ticker.C:
			_, _ = io.WriteString(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func (h *mcpHTTPHandler) sessionSSEExternal(w http.ResponseWriter, r *http.Request, session *mcpHTTPSession, lastID string, flusher http.Flusher) {
	cursor := strings.TrimSpace(lastID)
	if cursor == "" {
		cursor = "0-0"
	}
	for {
		events, err := h.config.EventStore.Read(r.Context(), session.id, cursor, h.config.SSEHeartbeat)
		if err != nil {
			if r.Context().Err() != nil {
				return
			}
			message := "event store unavailable"
			if errors.Is(err, ErrEventCursorExpired) {
				message = "event cursor expired; reinitialize the MCP session"
			}
			payload, _ := json.Marshal(map[string]string{"error": message})
			writeSSEEvent(w, mcpSSEEvent{id: "", data: payload})
			flusher.Flush()
			return
		}
		if len(events) == 0 {
			_, _ = io.WriteString(w, ": heartbeat\n\n")
			flusher.Flush()
			continue
		}
		for _, event := range events {
			writeSSEEvent(w, mcpSSEEvent{id: event.ID, data: event.Data})
			cursor = event.ID
		}
		flusher.Flush()
	}
}

func (h *mcpHTTPHandler) writeMCPSSE(w http.ResponseWriter, value map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("MCP-Protocol-Version", mcpCurrentProtocol)
	data, _ := json.Marshal(value)
	writeSSEEvent(w, mcpSSEEvent{id: strconv.FormatInt(time.Now().UnixNano(), 36), data: data})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeSSEEvent(w io.Writer, event mcpSSEEvent) {
	if event.id != "" {
		_, _ = io.WriteString(w, "id: "+event.id+"\n")
	}
	_, _ = io.WriteString(w, "event: message\n")
	for _, line := range strings.Split(string(event.data), "\n") {
		_, _ = io.WriteString(w, "data: "+line+"\n")
	}
	_, _ = io.WriteString(w, "\n")
}

func (h *mcpHTTPHandler) persistBinding(w http.ResponseWriter, session OAuthSession, request mcpRequest, response map[string]any) {
	if h.config.OAuth == nil || requestToolName(request) != "bind_project" || response == nil || response["error"] != nil {
		return
	}
	repository, ref, ok := bindingFromResponse(response)
	if !ok {
		return
	}
	if err := h.config.OAuth.setBinding(w, session, repository, ref); err != nil {
		h.config.AuditLogger.Warn("mcp binding persistence failed", "subject", session.Subject, "error", err)
	}
	if h.config.ControlPlane != nil {
		projectID := projectKey(repository, ref)
		if err := h.config.ControlPlane.UpsertProject(context.Background(), projectID, session.Subject, repository, ref, session.InstallationID); err != nil {
			h.config.AuditLogger.Warn("control plane project write failed", "subject", session.Subject, "repository", repository, "ref", ref, "error", err)
		}
	}
}

func projectKey(repository, ref string) string {
	digest := sha256.Sum256([]byte(repository + "@" + ref))
	return hex.EncodeToString(digest[:])
}

func bindingFromResponse(response map[string]any) (string, string, bool) {
	result, ok := response["result"].(map[string]any)
	if !ok {
		return "", "", false
	}
	value, ok := result["structuredContent"].(map[string]any)
	if !ok {
		return "", "", false
	}
	repository, _ := value["repository"].(string)
	ref, _ := value["ref"].(string)
	return strings.TrimSpace(repository), strings.TrimSpace(ref), repository != "" && ref != ""
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
	message := "Code Relay MCP gateway is running.\n"
	if h.config.OAuth == nil {
		message += "Use /healthz to check status.\n"
	} else if session, err := h.config.OAuth.Authenticate(r); err != nil {
		message += "OAuth session: not authenticated. Use /auth/github to sign in.\n"
	} else if session.InstallationID <= 0 {
		message += "OAuth session: authenticated. GitHub App installation: not bound. Use /auth/github/install.\n"
	} else {
		message += "OAuth session: authenticated. GitHub App installation: bound.\n"
	}
	_, _ = io.WriteString(w, message)
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
					props["repository"] = map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$", "description": "Optional when the active ChatGPT project context supplies the repository."}
					props["ref"] = map[string]any{"type": "string", "pattern": "^refs/heads/[A-Za-z0-9._/-]+$", "description": "Optional when the active ChatGPT project context supplies the branch."}
					props["project_context"] = map[string]any{"type": "object", "properties": map[string]any{"repository": map[string]any{"type": "string"}, "ref": map[string]any{"type": "string"}}, "description": "Project context supplied by the ChatGPT host; explicit repository/ref values take precedence."}
				}
			}
		}
	}
	return tools
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
	w.Header().Set("MCP-Protocol-Version", mcpCurrentProtocol)
	_ = json.NewEncoder(w).Encode(value)
}

func clientAddress(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
