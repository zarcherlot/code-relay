package relay

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOAuthAuthorizationCodePKCEExchange(t *testing.T) {
	redirectURI := "https://client.example/callback"
	service, err := NewOAuthService(OAuthConfig{
		ClientID:                  "github-client",
		ClientSecret:              "github-secret",
		RedirectURL:               "https://relay.example/auth/github/callback",
		SessionSecret:             strings.Repeat("s", 32),
		AppSlug:                   "code-relay",
		SessionStore:              NewMemorySessionStore(),
		AuthorizationClientID:     "mcp-client",
		AuthorizationRedirectURLs: []string{redirectURI},
	})
	if err != nil {
		t.Fatal(err)
	}
	verifier := "test-verifier-abcdefghijklmnopqrstuvwxyz"
	state := oauthState{ClientID: "mcp-client", RedirectURI: redirectURI, CodeChallenge: pkceChallenge(verifier)}
	session := OAuthSession{Subject: "17", Login: "alice", AccessToken: "github-token", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	code, err := service.issueAuthorizationCode(session, state)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {"mcp-client"},
		"redirect_uri":  {redirectURI},
		"code":          {code},
		"code_verifier": {verifier},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	if !service.ServeHTTP(res, req) || res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"access_token"`) {
		t.Fatalf("token exchange status/body = %d/%s", res.Code, res.Body.String())
	}

	replay := httptest.NewRecorder()
	replayReq := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	replayReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	service.ServeHTTP(replay, replayReq)
	if replay.Code != http.StatusBadRequest || !strings.Contains(replay.Body.String(), "invalid_grant") {
		t.Fatalf("authorization code replay status/body = %d/%s", replay.Code, replay.Body.String())
	}

	bad := httptest.NewRecorder()
	badForm := url.Values{}
	for key, values := range form {
		badForm[key] = append([]string(nil), values...)
	}
	badForm.Set("code_verifier", "wrong")
	// A second code proves PKCE is checked before issuance.
	second, err := service.issueAuthorizationCode(session, state)
	if err != nil {
		t.Fatal(err)
	}
	badForm.Set("code", second)
	badReq := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(badForm.Encode()))
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	service.ServeHTTP(bad, badReq)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("wrong verifier status = %d", bad.Code)
	}

}

func TestOAuthAuthorizeRejectsUnregisteredRedirect(t *testing.T) {
	service, err := NewOAuthService(OAuthConfig{ClientID: "github", ClientSecret: "secret", RedirectURL: "https://relay.example/callback", SessionSecret: strings.Repeat("s", 32), AppSlug: "code-relay", AuthorizationClientID: "mcp-client", AuthorizationRedirectURLs: []string{"https://client.example/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?response_type=code&client_id=mcp-client&redirect_uri=https%3A%2F%2Fevil.example%2Fcb&code_challenge=x&code_challenge_method=S256", nil)
	res := httptest.NewRecorder()
	service.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("unregistered redirect status = %d", res.Code)
	}
}

func TestOAuthAuthorizeStartsGitHubPKCE(t *testing.T) {
	redirectURI := "https://client.example/callback"
	service, err := NewOAuthService(OAuthConfig{ClientID: "github", ClientSecret: "secret", RedirectURL: "https://relay.example/callback", SessionSecret: strings.Repeat("s", 32), AppSlug: "code-relay", AuthorizationClientID: "mcp-client", AuthorizationRedirectURLs: []string{redirectURI}, GitHubOAuthURL: "https://github.example"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?response_type=code&client_id=mcp-client&redirect_uri=https%3A%2F%2Fclient.example%2Fcallback&state=abc&code_challenge=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&code_challenge_method=S256", nil)
	res := httptest.NewRecorder()
	service.ServeHTTP(res, req)
	if res.Code != http.StatusFound || !strings.HasPrefix(res.Header().Get("Location"), "https://github.example/login/oauth/authorize?") {
		t.Fatalf("authorize redirect status/location = %d/%q", res.Code, res.Header().Get("Location"))
	}
}
