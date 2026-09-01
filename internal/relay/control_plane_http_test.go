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

type captureControlPlane struct {
	tenantCalls  int
	auditCalls   int
	projectCalls int
}

func (p *captureControlPlane) EnsureTenant(context.Context, string, string) error {
	p.tenantCalls++
	return nil
}

func (p *captureControlPlane) UpsertProject(context.Context, string, string, string, string, int64) error {
	p.projectCalls++
	return nil
}

func (p *captureControlPlane) AppendAudit(context.Context, string, string, string, string, string, []byte) error {
	p.auditCalls++
	return nil
}

func TestMCPHTTPWritesControlPlaneAudit(t *testing.T) {
	service, err := NewOAuthService(OAuthConfig{ClientID: "c", ClientSecret: "s", RedirectURL: "https://relay.example/callback", SessionSecret: strings.Repeat("s", 32), AppSlug: "code-relay", SecureCookies: false})
	if err != nil {
		t.Fatal(err)
	}
	backend := &captureRemoteBackend{}
	controlPlane := &captureControlPlane{}
	handler, err := MCPHTTPHandler(MCPHTTPConfig{OAuth: service, RemoteBackend: backend, ControlPlane: controlPlane})
	if err != nil {
		t.Fatal(err)
	}
	cookieRecorder := httptest.NewRecorder()
	if err := service.setSession(cookieRecorder, OAuthSession{Subject: "17", Login: "alice", AccessToken: "oauth", InstallationID: 7, Repository: "acme/demo", Ref: "refs/heads/main", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	cookie := cookieRecorder.Result().Cookies()[0]
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"doctor","arguments":{}}}`)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || controlPlane.tenantCalls != 1 || controlPlane.auditCalls != 1 {
		t.Fatalf("status/control-plane calls = %d/%d/%d", res.Code, controlPlane.tenantCalls, controlPlane.auditCalls)
	}
}
