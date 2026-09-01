package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOAuthMetadata(t *testing.T) {
	service, err := NewOAuthService(OAuthConfig{ClientID: "c", ClientSecret: "s", RedirectURL: "https://relay.example/auth/github/callback", SessionSecret: strings.Repeat("s", 32), AppSlug: "code-relay", IssuerURL: "https://relay.example", ResourceURL: "https://relay.example/mcp", OAuthScopes: []string{"relay"}})
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	service.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))
	var metadata protectedResourceMetadata
	if err := json.NewDecoder(res.Body).Decode(&metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Resource != "https://relay.example/mcp" || len(metadata.AuthorizationServers) != 1 || metadata.BearerMethodsSupported[0] != "header" {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}

	res = httptest.NewRecorder()
	service.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))
	var auth authorizationServerMetadata
	if err := json.NewDecoder(res.Body).Decode(&auth); err != nil {
		t.Fatal(err)
	}
	if auth.AuthorizationEndpoint != "https://relay.example/auth/github" || auth.CodeChallengeMethodsSupported[0] != "S256" {
		t.Fatalf("unexpected auth metadata: %+v", auth)
	}
}

func TestOAuthServiceOpaqueSession(t *testing.T) {
	store := NewMemorySessionStore()
	service, err := NewOAuthService(OAuthConfig{ClientID: "c", ClientSecret: "s", RedirectURL: "https://relay.example/auth/github/callback", SessionSecret: strings.Repeat("s", 32), AppSlug: "code-relay", SessionStore: store})
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	session := OAuthSession{Subject: "17", Login: "alice", AccessToken: "github-secret", ExpiresAt: time.Now().Add(time.Minute).Unix()}
	if err := service.setSession(res, session); err != nil {
		t.Fatal(err)
	}
	cookie := res.Result().Cookies()[0]
	if cookie.Value == "" || cookie.Value == session.AccessToken || strings.Contains(cookie.Value, "{") {
		t.Fatalf("expected opaque cookie, got %q", cookie.Value)
	}
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.AddCookie(cookie)
	got, err := service.Authenticate(req)
	if err != nil || got.AccessToken != session.AccessToken {
		t.Fatalf("authenticate: %+v, %v", got, err)
	}
	if err := store.Revoke(context.Background(), cookie.Value); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(req); err == nil {
		t.Fatal("revoked session authenticated")
	}
}

func TestOAuthOpaqueAccessTokenRotation(t *testing.T) {
	store := NewMemorySessionStore()
	service, err := NewOAuthService(OAuthConfig{ClientID: "c", ClientSecret: "s", RedirectURL: "https://relay.example/auth/github/callback", SessionSecret: strings.Repeat("s", 32), AppSlug: "code-relay", SessionStore: store})
	if err != nil {
		t.Fatal(err)
	}
	session := OAuthSession{Subject: "17", AccessToken: "github-secret", ExpiresAt: time.Now().Add(time.Minute).Unix()}
	token, err := service.IssueAccessToken(context.Background(), session, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := service.RotateAccessToken(context.Background(), token, session, time.Minute)
	if err != nil || replacement == token {
		t.Fatalf("rotation: %q, %v", replacement, err)
	}
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+replacement)
	if _, err := service.Authenticate(req); err != nil {
		t.Fatalf("rotated token rejected: %v", err)
	}
	oldReq := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	oldReq.Header.Set("Authorization", "Bearer "+token)
	if _, err := service.Authenticate(oldReq); err == nil {
		t.Fatal("old token accepted after rotation")
	}
}
