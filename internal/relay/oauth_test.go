package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOAuthPKCEAndEncryptedSession(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != "" || r.Form.Get("code_verifier") == "" {
				t.Fatalf("unexpected OAuth exchange form: %v", r.Form)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "oauth-secret-token", "token_type": "bearer"})
		case "/user":
			if r.Header.Get("Authorization") != "Bearer oauth-secret-token" {
				t.Fatal("provider did not receive bearer token")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 17, "login": "alice"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	service, err := NewOAuthService(OAuthConfig{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://relay.example/auth/github/callback", SessionSecret: strings.Repeat("s", 32), AppSlug: "code-relay", GitHubOAuthURL: " " + provider.URL + " ", GitHubAPIURL: " " + provider.URL + " ", SecureCookies: false})
	if err != nil {
		t.Fatal(err)
	}
	start := httptest.NewRecorder()
	service.ServeHTTP(start, httptest.NewRequest(http.MethodGet, "/auth/github?next=/welcome", nil))
	location, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	query := location.Query()
	if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" || query.Get("state") == "" {
		t.Fatalf("missing PKCE parameters: %s", location.String())
	}
	stateCookie := start.Result().Cookies()[0]

	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/github/callback?code=abc&state="+url.QueryEscape(query.Get("state")), nil)
	callbackReq.AddCookie(stateCookie)
	callback := httptest.NewRecorder()
	service.ServeHTTP(callback, callbackReq)
	if callback.Code != http.StatusFound || callback.Header().Get("Location") != "/welcome" {
		t.Fatalf("unexpected callback: %d %s", callback.Code, callback.Header().Get("Location"))
	}
	var sessionCookie *http.Cookie
	for _, cookie := range callback.Result().Cookies() {
		if cookie.Name == defaultSessionCookie {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil || strings.Contains(sessionCookie.Value, "oauth-secret-token") {
		t.Fatal("session cookie missing or leaked the OAuth token")
	}
	authReq := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	authReq.AddCookie(sessionCookie)
	session, err := service.Authenticate(authReq)
	if err != nil {
		t.Fatal(err)
	}
	if session.Login != "alice" || session.Subject != "17" || session.AccessToken != "oauth-secret-token" {
		t.Fatalf("unexpected session: %+v", session)
	}
}

func TestOAuthRejectsStateReplayOrMismatch(t *testing.T) {
	service, err := NewOAuthService(OAuthConfig{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://relay.example/auth/github/callback", SessionSecret: strings.Repeat("s", 32), AppSlug: "code-relay"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?code=abc&state=wrong", nil)
	res := httptest.NewRecorder()
	service.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected state rejection, got %d", res.Code)
	}
}
