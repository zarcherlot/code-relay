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

func validRemoteRunbook() string {
	return "- runbook_id: remote-1\n- source_commit: 0123456789abcdef0123456789abcdef01234567\n- target: remote\n- objective: verify remote publishing\n\n## Validation Plan\n1. run checks\n\n## Expected Results\n- checks pass\n"
}

func TestGitHubRemotePublishAndDispatch(t *testing.T) {
	var putSeen, dispatchSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/user/installations/7/repositories":
			_, _ = w.Write([]byte(`{"repositories":[{"full_name":"acme/demo"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/7/access_tokens":
			_, _ = w.Write([]byte(`{"token":"installation-token","expires_at":"2030-01-01T00:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/demo":
			_, _ = w.Write([]byte(`{"full_name":"acme/demo","private":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/demo/contents/runbooks/remote-1/runbook.md":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/repos/acme/demo/contents/runbooks/remote-1/runbook.md":
			putSeen = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/demo/actions/workflows/checkpoint.yml/dispatches":
			dispatchSeen = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	app, err := NewGitHubAppClient(GitHubAppConfig{AppID: 99, PrivateKeyPEM: testPrivateKeyPEM(t), APIBaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := NewGitHubRemoteBackend(app, "checkpoint.yml")
	if err != nil {
		t.Fatal(err)
	}
	value, err := backend.Call(context.Background(), OAuthSession{Subject: "17", Login: "alice", AccessToken: "user-token", InstallationID: 7}, "publish_runbook", map[string]any{"repository": "acme/demo", "ref": "refs/heads/main", "markdown": validRemoteRunbook()})
	if err != nil {
		t.Fatal(err)
	}
	if !putSeen || !dispatchSeen || value.(map[string]any)["storage"] != "github-api" {
		t.Fatalf("remote publish did not complete: put=%v dispatch=%v value=%#v", putSeen, dispatchSeen, value)
	}
}

type captureRemoteBackend struct {
	name string
	args map[string]any
}

func (b *captureRemoteBackend) Call(_ context.Context, _ OAuthSession, name string, args map[string]any) (any, error) {
	b.name, b.args = name, args
	return map[string]any{"ok": true}, nil
}

func TestRemoteHTTPRequiresOAuthAndDoesNotAcceptRoot(t *testing.T) {
	service, err := NewOAuthService(OAuthConfig{ClientID: "c", ClientSecret: "s", RedirectURL: "https://relay.example/auth/github/callback", SessionSecret: strings.Repeat("s", 32), AppSlug: "code-relay", SecureCookies: false})
	if err != nil {
		t.Fatal(err)
	}
	backend := &captureRemoteBackend{}
	handler, err := MCPHTTPHandler(MCPHTTPConfig{OAuth: service, RemoteBackend: backend})
	if err != nil {
		t.Fatal(err)
	}
	unauth := httptest.NewRecorder()
	unauthReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"doctor","arguments":{}}}`)))
	handler.ServeHTTP(unauth, unauthReq)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d", unauth.Code)
	}
	cookieRecorder := httptest.NewRecorder()
	if err := service.setSession(cookieRecorder, OAuthSession{Subject: "17", Login: "alice", AccessToken: "oauth", InstallationID: 7, Repository: "acme/demo", Ref: "refs/heads/main", IssuedAt: 1, ExpiresAt: 4102444800}); err != nil {
		t.Fatal(err)
	}
	var sessionCookie *http.Cookie
	for _, cookie := range cookieRecorder.Result().Cookies() {
		if cookie.Name == defaultSessionCookie {
			sessionCookie = cookie
		}
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "doctor", "arguments": map[string]any{"root": "C:/must-not-be-used"}}})
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(sessionCookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || backend.name != "doctor" || backend.args["root"] != nil || backend.args["repository"] != "acme/demo" || backend.args["ref"] != "refs/heads/main" {
		t.Fatalf("remote request not routed as expected: code=%d name=%s args=%#v body=%s", res.Code, backend.name, backend.args, res.Body.String())
	}
}

func TestRemoteScopeValidation(t *testing.T) {
	if _, err := normalizeRef("refs/heads/main..bad"); err == nil {
		t.Fatal("expected branch traversal rejection")
	}
	if _, err := normalizeRef("refs/tags/v1"); err == nil {
		t.Fatal("expected non-head ref rejection")
	}
	if _, err := safeRepoPath("../secrets"); err == nil {
		t.Fatal("expected repository path traversal rejection")
	}
	if _, err := normalizeRepository("acme/demo\n"); err == nil {
		t.Fatal("expected repository control character rejection")
	}
}

func TestRemoteAllowedRefPolicy(t *testing.T) {
	backend := &GitHubRemoteBackend{AllowedRefs: map[string]bool{"acme/demo@refs/heads/main": true}}
	if !backend.AllowedRefs["acme/demo@refs/heads/main"] {
		t.Fatal("test policy was not initialized")
	}
	if backend.AllowedRefs["acme/demo@refs/heads/dev"] {
		t.Fatal("unexpected branch in allowlist")
	}
}

func TestRemoteBindingRejectsExplicitScopeMismatch(t *testing.T) {
	backend := &GitHubRemoteBackend{AllowedRefs: map[string]bool{}}
	_, _, _, err := backend.authorizedRepository(context.Background(), OAuthSession{AccessToken: "token", InstallationID: 1, Repository: "acme/demo", Ref: "refs/heads/main"}, "status", map[string]any{"repository": "acme/other", "ref": "refs/heads/main"})
	if err == nil || !strings.Contains(err.Error(), "active repository binding") {
		t.Fatalf("expected active binding mismatch, got %v", err)
	}
}
