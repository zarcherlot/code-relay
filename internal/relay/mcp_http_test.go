package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPHTTPHealthAndAuth(t *testing.T) {
	handler, err := MCPHTTPHandler(MCPHTTPConfig{Root: t.TempDir(), BearerToken: strings.Repeat("t", 32), DomainChallenge: "challenge-token"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected health response: %d %s", recorder.Code, recorder.Body.String())
	}
	home := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, home)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "gateway is running") {
		t.Fatalf("unexpected home response: %d %s", recorder.Code, recorder.Body.String())
	}
	challenge := httptest.NewRequest(http.MethodGet, "/.well-known/openai-apps-challenge", nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, challenge)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "challenge-token" {
		t.Fatalf("unexpected domain challenge response: %d %q", recorder.Code, recorder.Body.String())
	}

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`)
	request = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status = %d", recorder.Code)
	}

	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMCPHTTPReadiness(t *testing.T) {
	handler, err := MCPHTTPHandler(MCPHTTPConfig{Root: t.TempDir(), BearerToken: strings.Repeat("t", 32), Readiness: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"ready"`) {
		t.Fatalf("unexpected readiness response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestMCPHTTPFiltersToolsAndPinsRoot(t *testing.T) {
	root := t.TempDir()
	token := strings.Repeat("t", 32)
	handler, err := MCPHTTPHandler(MCPHTTPConfig{Root: root, BearerToken: token})
	if err != nil {
		t.Fatal(err)
	}
	listBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(listBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("tools/list status = %d", recorder.Code)
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	result := response["result"].(map[string]any)
	tools := result["tools"].([]any)
	for _, item := range tools {
		tool := item.(map[string]any)
		name := tool["name"].(string)
		annotations, ok := tool["annotations"].(map[string]any)
		if !ok || annotations["readOnlyHint"] == nil || annotations["openWorldHint"] == nil || annotations["destructiveHint"] == nil {
			t.Fatalf("tool %s is missing MCP annotations", name)
		}
		if name == "join_checkpoint" || name == "create_checkpoint_invite" {
			t.Fatalf("unsafe remote tool advertised: %s", name)
		}
	}

	callBody := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"status","arguments":{"root":"C:\\outside"}}}`)
	request = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(callBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("tools/call status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), `C:\\outside`) {
		t.Fatal("remote caller-controlled root leaked into the response")
	}
}

func TestMCPHTTPRejectsUnconfiguredAndOversizedRequests(t *testing.T) {
	if _, err := MCPHTTPHandler(MCPHTTPConfig{Root: t.TempDir(), BearerToken: "short"}); err == nil {
		t.Fatal("expected short token rejection")
	}
	handler, err := MCPHTTPHandler(MCPHTTPConfig{Root: t.TempDir(), BearerToken: strings.Repeat("t", 32), MaxBodyBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(strings.Repeat("x", 32)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d", recorder.Code)
	}
}
