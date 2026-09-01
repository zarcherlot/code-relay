package relay

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamableHTTPInitializeSSECreatesOwnedSession(t *testing.T) {
	token := strings.Repeat("t", 32)
	handler, err := MCPHTTPHandler(MCPHTTPConfig{Root: t.TempDir(), BearerToken: token})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	req := httptest.NewRequest(http.MethodPost, "http://relay.example/mcp", bytes.NewReader(body))
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("initialize status/content type = %d/%q", res.Code, res.Header().Get("Content-Type"))
	}
	sessionID := res.Header().Get("Mcp-Session-Id")
	if sessionID == "" || !strings.Contains(res.Body.String(), "event: message") {
		t.Fatalf("initialize did not return SSE response and session id: id=%q body=%s", sessionID, res.Body.String())
	}

	wrongOwner := httptest.NewRequest(http.MethodPost, "http://relay.example/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":2,"method":"ping","params":{}}`)))
	wrongOwner.RemoteAddr = "192.0.2.11:1234"
	wrongOwner.Header.Set("Authorization", "Bearer "+token)
	wrongOwner.Header.Set("Content-Type", "application/json")
	wrongOwner.Header.Set("Mcp-Session-Id", sessionID)
	wrongRes := httptest.NewRecorder()
	handler.ServeHTTP(wrongRes, wrongOwner)
	if wrongRes.Code != http.StatusForbidden {
		t.Fatalf("cross-owner session status = %d", wrongRes.Code)
	}
}

func TestStreamableHTTPOriginAllowlistAndGETAccept(t *testing.T) {
	token := strings.Repeat("t", 32)
	handler, err := MCPHTTPHandler(MCPHTTPConfig{Root: t.TempDir(), BearerToken: token, AllowedOrigins: []string{"https://chatgpt.com"}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://relay.example/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://chatgpt.com")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Header().Get("Access-Control-Allow-Origin") != "https://chatgpt.com" {
		t.Fatalf("allowlisted origin status/cors = %d/%q", res.Code, res.Header().Get("Access-Control-Allow-Origin"))
	}
	bad := httptest.NewRequest(http.MethodPost, "https://relay.example/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`)))
	bad.Header.Set("Authorization", "Bearer "+token)
	bad.Header.Set("Content-Type", "application/json")
	bad.Header.Set("Origin", "https://evil.example")
	badRes := httptest.NewRecorder()
	handler.ServeHTTP(badRes, bad)
	if badRes.Code != http.StatusForbidden {
		t.Fatalf("non-allowlisted origin status = %d", badRes.Code)
	}

	h := handler.(*mcpHTTPHandler)
	id := h.newSession("192.0.2.1")
	get := httptest.NewRequest(http.MethodGet, "https://relay.example/mcp", nil)
	get.RemoteAddr = "192.0.2.1:1234"
	get.Header.Set("Authorization", "Bearer "+token)
	get.Header.Set("Mcp-Session-Id", id)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, get)
	if getRes.Code != http.StatusNotAcceptable {
		t.Fatalf("GET without SSE Accept status = %d", getRes.Code)
	}
}

func TestStreamableHTTPSessionExpiry(t *testing.T) {
	handler, err := MCPHTTPHandler(MCPHTTPConfig{Root: t.TempDir(), BearerToken: strings.Repeat("t", 32)})
	if err != nil {
		t.Fatal(err)
	}
	h := handler.(*mcpHTTPHandler)
	id := h.newSession("owner")
	h.mu.Lock()
	h.sessions[id].expiresAt = time.Now().Add(-time.Second)
	h.mu.Unlock()
	if h.lookupSession(id) != nil {
		t.Fatal("expired session remained available")
	}
}

func TestStreamableHTTPDrainRejectsNewRequests(t *testing.T) {
	token := strings.Repeat("t", 32)
	handler, err := MCPHTTPHandler(MCPHTTPConfig{Root: t.TempDir(), BearerToken: token})
	if err != nil {
		t.Fatal(err)
	}
	drainable := handler.(interface {
		BeginDrain()
		WaitForDrain(context.Context) error
	})
	drainable.BeginDrain()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable || res.Header().Get("Retry-After") == "" {
		t.Fatalf("draining request status/retry = %d/%q", res.Code, res.Header().Get("Retry-After"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := drainable.WaitForDrain(ctx); err != nil {
		t.Fatalf("drain wait failed: %v", err)
	}
}
