package relay

// OAuth 2.1 helpers for the hosted MCP gateway.  The implementation uses
// GitHub's authorization-code flow with PKCE and encrypted, short-lived
// cookies.  No OAuth tokens or state are written to the gateway filesystem;
// this makes the default deployment safe for ephemeral containers.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultGitHubOAuthURL = "https://github.com"
	defaultGitHubAPIURL   = "https://api.github.com"
	defaultSessionCookie  = "code_relay_session"
	defaultStateCookie    = "code_relay_oauth_state"
)

type OAuthConfig struct {
	ClientID       string
	ClientSecret   string
	RedirectURL    string
	SessionSecret  string
	AppSlug        string
	GitHubOAuthURL string
	GitHubAPIURL   string
	SessionCookie  string
	StateCookie    string
	CookieDomain   string
	SecureCookies  bool
	// SessionStore enables opaque, server-side sessions. When nil, encrypted
	// cookies remain the backwards-compatible default.
	SessionStore              SessionStore
	IssuerURL                 string
	ResourceURL               string
	AuthorizationServerURL    string
	TokenEndpointURL          string
	AuthorizationClientID     string
	AuthorizationRedirectURLs []string
	AuthorizationCodeTTL      time.Duration
	OAuthScopes               []string
}

type OAuthSession struct {
	Subject        string `json:"sub"`
	Login          string `json:"login"`
	AccessToken    string `json:"access_token"`
	InstallationID int64  `json:"installation_id,omitempty"`
	Repository     string `json:"repository,omitempty"`
	Ref            string `json:"ref,omitempty"`
	IssuedAt       int64  `json:"iat"`
	ExpiresAt      int64  `json:"exp"`
}

type oauthState struct {
	State         string `json:"state"`
	Verifier      string `json:"verifier"`
	Next          string `json:"next"`
	Repository    string `json:"repository,omitempty"`
	Ref           string `json:"ref,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
	RedirectURI   string `json:"redirect_uri,omitempty"`
	ClientState   string `json:"client_state,omitempty"`
	CodeChallenge string `json:"code_challenge,omitempty"`
	Expires       int64  `json:"exp"`
}

type oauthAuthorizationCode struct {
	Session     OAuthSession
	ClientID    string
	RedirectURI string
	Challenge   string
	ExpiresAt   time.Time
}

type githubUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

type githubOAuthToken struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

type OAuthService struct {
	config OAuthConfig
	client *http.Client
	key    []byte
	store  SessionStore
	codeMu sync.Mutex
	codes  map[string]oauthAuthorizationCode
}

// IssueAccessToken creates an opaque bearer credential backed by the
// configured SessionStore. It is intentionally unavailable with legacy
// encrypted-cookie mode; hosted deployments should configure a shared store.
func (s *OAuthService) IssueAccessToken(ctx context.Context, session OAuthSession, ttl time.Duration) (string, error) {
	if s.store == nil {
		return "", errors.New("opaque access tokens require a session store")
	}
	return s.store.Create(ctx, session, ttl)
}

// RotateAccessToken atomically revokes an existing opaque credential and
// creates a replacement with the supplied session and lifetime.
func (s *OAuthService) RotateAccessToken(ctx context.Context, token string, session OAuthSession, ttl time.Duration) (string, error) {
	if s.store == nil {
		return "", errors.New("opaque access tokens require a session store")
	}
	return s.store.Rotate(ctx, token, session, ttl)
}

func NewOAuthService(config OAuthConfig) (*OAuthService, error) {
	config.ClientID = strings.TrimSpace(config.ClientID)
	config.ClientSecret = strings.TrimSpace(config.ClientSecret)
	config.RedirectURL = strings.TrimSpace(config.RedirectURL)
	config.SessionSecret = strings.TrimSpace(config.SessionSecret)
	config.AppSlug = strings.TrimSpace(config.AppSlug)
	config.GitHubOAuthURL = strings.TrimSpace(config.GitHubOAuthURL)
	config.GitHubAPIURL = strings.TrimSpace(config.GitHubAPIURL)
	config.IssuerURL = strings.TrimSpace(config.IssuerURL)
	config.ResourceURL = strings.TrimSpace(config.ResourceURL)
	config.AuthorizationServerURL = strings.TrimSpace(config.AuthorizationServerURL)
	config.TokenEndpointURL = strings.TrimSpace(config.TokenEndpointURL)
	config.AuthorizationClientID = strings.TrimSpace(config.AuthorizationClientID)
	for i, redirect := range config.AuthorizationRedirectURLs {
		config.AuthorizationRedirectURLs[i] = strings.TrimSpace(redirect)
	}
	if config.AuthorizationCodeTTL <= 0 {
		config.AuthorizationCodeTTL = 60 * time.Second
	}
	if config.GitHubOAuthURL == "" {
		config.GitHubOAuthURL = defaultGitHubOAuthURL
	}
	if config.GitHubAPIURL == "" {
		config.GitHubAPIURL = defaultGitHubAPIURL
	}
	if config.SessionCookie == "" {
		config.SessionCookie = defaultSessionCookie
	}
	if config.StateCookie == "" {
		config.StateCookie = defaultStateCookie
	}
	if config.ClientID == "" || config.ClientSecret == "" || config.RedirectURL == "" {
		return nil, errors.New("GitHub OAuth requires client id, client secret, and redirect URL")
	}
	if len(config.SessionSecret) < 32 {
		return nil, errors.New("CODE_RELAY_SESSION_SECRET must contain at least 32 characters")
	}
	if config.AppSlug == "" {
		return nil, errors.New("GitHub App slug is required for installation flow")
	}
	key := sha256.Sum256([]byte(config.SessionSecret))
	return &OAuthService{config: config, client: &http.Client{Timeout: 15 * time.Second}, key: key[:], store: config.SessionStore, codes: make(map[string]oauthAuthorizationCode)}, nil
}

// ServeHTTP serves the OAuth endpoints.  It is intended to be mounted under
// the same host as /mcp so the callback and session cookie share a domain.
func (s *OAuthService) ServeHTTP(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/auth/github":
		s.start(w, r)
		return true
	case "/auth/github/callback":
		s.callback(w, r)
		return true
	case "/auth/github/install":
		s.install(w, r)
		return true
	case "/auth/github/app-callback":
		s.appCallback(w, r)
		return true
	case "/auth/logout":
		s.logout(w, r)
		return true
	case "/oauth/authorize":
		s.authorize(w, r)
		return true
	case "/oauth/token":
		s.token(w, r)
		return true
	case "/.well-known/oauth-protected-resource", "/mcp/.well-known/oauth-protected-resource", "/.well-known/oauth-authorization-server":
		s.metadata(w, r)
		return true
	default:
		return false
	}
}

func (s *OAuthService) Authenticate(r *http.Request) (OAuthSession, error) {
	if s.store != nil {
		if authorization := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
			id := strings.TrimSpace(authorization[len("bearer "):])
			if id == "" {
				return OAuthSession{}, errors.New("invalid bearer token")
			}
			if session, err := s.store.Get(r.Context(), id); err == nil {
				return validateOAuthSession(session)
			}
			return OAuthSession{}, errors.New("invalid bearer token")
		}
	}
	cookie, err := r.Cookie(s.config.SessionCookie)
	if err != nil {
		return OAuthSession{}, errors.New("authentication required")
	}
	if s.store != nil {
		session, err := s.store.Get(r.Context(), cookie.Value)
		if err != nil {
			return OAuthSession{}, errors.New("invalid session")
		}
		return validateOAuthSession(session)
	}
	var session OAuthSession
	if err := s.open(cookie.Value, &session); err != nil {
		return OAuthSession{}, errors.New("invalid session")
	}
	return validateOAuthSession(session)
}

// authorize starts the MCP OAuth authorization-code flow. GitHub remains the
// identity provider; this endpoint adds the MCP client's redirect and PKCE
// binding around that provider login.
func (s *OAuthService) authorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	clientID := strings.TrimSpace(r.URL.Query().Get("client_id"))
	redirectURI := strings.TrimSpace(r.URL.Query().Get("redirect_uri"))
	if s.config.AuthorizationClientID == "" || clientID != s.config.AuthorizationClientID || !s.allowedRedirectURI(redirectURI) {
		http.Error(w, "unauthorized OAuth client", http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("response_type") != "code" || r.URL.Query().Get("code_challenge_method") != "S256" {
		http.Error(w, "authorization code with S256 PKCE is required", http.StatusBadRequest)
		return
	}
	challenge := strings.TrimSpace(r.URL.Query().Get("code_challenge"))
	if challenge == "" || len(challenge) > 256 {
		http.Error(w, "code_challenge is required", http.StatusBadRequest)
		return
	}
	state := randomToken(32)
	verifier := randomToken(48)
	payload := oauthState{State: state, Verifier: verifier, ClientID: clientID, RedirectURI: redirectURI, ClientState: r.URL.Query().Get("state"), CodeChallenge: challenge, Expires: time.Now().Add(10 * time.Minute).Unix()}
	value, err := s.seal(payload)
	if err != nil {
		http.Error(w, "oauth state unavailable", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, s.cookie(s.config.StateCookie, value, 600))
	query := url.Values{}
	query.Set("client_id", s.config.ClientID)
	query.Set("redirect_uri", s.config.RedirectURL)
	query.Set("response_type", "code")
	query.Set("scope", "read:user")
	query.Set("state", state)
	query.Set("code_challenge", pkceChallenge(verifier))
	query.Set("code_challenge_method", "S256")
	http.Redirect(w, r, strings.TrimRight(s.config.GitHubOAuthURL, "/")+"/login/oauth/authorize?"+query.Encode(), http.StatusFound)
}

func (s *OAuthService) allowedRedirectURI(value string) bool {
	for _, allowed := range s.config.AuthorizationRedirectURLs {
		if value != "" && value == allowed {
			return true
		}
	}
	return false
}

func (s *OAuthService) token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if s.store == nil {
		http.Error(w, "OAuth token service is not configured", http.StatusNotImplemented)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.oauthTokenError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if r.Form.Get("grant_type") != "authorization_code" {
		s.oauthTokenError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	clientID := strings.TrimSpace(r.Form.Get("client_id"))
	redirectURI := strings.TrimSpace(r.Form.Get("redirect_uri"))
	code := strings.TrimSpace(r.Form.Get("code"))
	verifier := strings.TrimSpace(r.Form.Get("code_verifier"))
	if clientID == "" || code == "" || verifier == "" || !s.allowedRedirectURI(redirectURI) || clientID != s.config.AuthorizationClientID {
		s.oauthTokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	s.codeMu.Lock()
	authCode, ok := s.codes[code]
	if ok {
		delete(s.codes, code)
	}
	s.codeMu.Unlock()
	if !ok || time.Now().After(authCode.ExpiresAt) || authCode.ClientID != clientID || authCode.RedirectURI != redirectURI || !constantTimeEqual(authCode.Challenge, pkceChallenge(verifier)) {
		s.oauthTokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	ttl := time.Until(time.Unix(authCode.Session.ExpiresAt, 0))
	accessToken, err := s.IssueAccessToken(r.Context(), authCode.Session, ttl)
	if err != nil {
		s.oauthTokenError(w, http.StatusInternalServerError, "server_error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": accessToken, "token_type": "Bearer", "expires_in": max(1, int(ttl/time.Second)), "scope": strings.Join(s.config.OAuthScopes, " ")})
}

func (s *OAuthService) oauthTokenError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func (s *OAuthService) issueAuthorizationCode(session OAuthSession, state oauthState) (string, error) {
	code := randomToken(32)
	s.codeMu.Lock()
	now := time.Now()
	for existing, record := range s.codes {
		if !now.Before(record.ExpiresAt) {
			delete(s.codes, existing)
		}
	}
	s.codes[code] = oauthAuthorizationCode{Session: session, ClientID: state.ClientID, RedirectURI: state.RedirectURI, Challenge: state.CodeChallenge, ExpiresAt: time.Now().Add(s.config.AuthorizationCodeTTL)}
	s.codeMu.Unlock()
	return code, nil
}

func validateOAuthSession(session OAuthSession) (OAuthSession, error) {
	if session.Subject == "" || session.AccessToken == "" || session.ExpiresAt <= time.Now().Unix() {
		return OAuthSession{}, errors.New("session expired")
	}
	return session, nil
}

func (s *OAuthService) start(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	next := safeNext(r.URL.Query().Get("next"))
	repository, ref, err := requestedBinding(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	state := randomToken(32)
	verifier := randomToken(48)
	payload := oauthState{State: state, Verifier: verifier, Next: next, Repository: repository, Ref: ref, Expires: time.Now().Add(10 * time.Minute).Unix()}
	value, err := s.seal(payload)
	if err != nil {
		http.Error(w, "oauth state unavailable", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, s.cookie(s.config.StateCookie, value, 600))
	query := url.Values{}
	query.Set("client_id", s.config.ClientID)
	query.Set("redirect_uri", s.config.RedirectURL)
	query.Set("response_type", "code")
	query.Set("scope", "read:user")
	query.Set("state", state)
	query.Set("code_challenge", pkceChallenge(verifier))
	query.Set("code_challenge_method", "S256")
	http.Redirect(w, r, strings.TrimRight(s.config.GitHubOAuthURL, "/")+"/login/oauth/authorize?"+query.Encode(), http.StatusFound)
}

func (s *OAuthService) callback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	stateCookie, err := r.Cookie(s.config.StateCookie)
	if err != nil {
		http.Error(w, "missing oauth state", http.StatusBadRequest)
		return
	}
	var state oauthState
	if err := s.open(stateCookie.Value, &state); err != nil || state.Expires <= time.Now().Unix() || !constantTimeEqual(state.State, r.URL.Query().Get("state")) {
		http.Error(w, "invalid oauth state", http.StatusBadRequest)
		return
	}
	if oauthErr := r.URL.Query().Get("error"); oauthErr != "" {
		http.Error(w, "GitHub OAuth denied", http.StatusUnauthorized)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		http.Error(w, "missing oauth code", http.StatusBadRequest)
		return
	}
	token, err := s.exchangeCode(r.Context(), code, state.Verifier)
	if err != nil {
		http.Error(w, "GitHub OAuth exchange failed: "+safeOAuthError(err), http.StatusBadGateway)
		return
	}
	user, err := s.githubUser(r.Context(), token)
	if err != nil || user.ID == 0 || user.Login == "" {
		http.Error(w, "GitHub identity lookup failed", http.StatusBadGateway)
		return
	}
	session := OAuthSession{Subject: strconv.FormatInt(user.ID, 10), Login: user.Login, AccessToken: token, Repository: state.Repository, Ref: state.Ref, IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(8 * time.Hour).Unix()}
	if session.Repository != "" {
		installationID, findErr := NewGitHubAppMembershipClient(s.config.GitHubAPIURL).FindUserInstallationForRepository(r.Context(), token, s.config.AppSlug, session.Repository)
		if findErr == nil {
			session.InstallationID = installationID
		} else if !errors.Is(findErr, errInstallationNotFound) {
			http.Error(w, "GitHub App installation lookup failed", http.StatusBadGateway)
			return
		}
	}
	if err := s.setSession(w, session); err != nil {
		http.Error(w, "session unavailable", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, s.cookie(s.config.StateCookie, "", -1))
	if state.ClientID != "" {
		authorizationCode, codeErr := s.issueAuthorizationCode(session, state)
		if codeErr != nil {
			http.Error(w, "authorization code unavailable", http.StatusInternalServerError)
			return
		}
		redirect, _ := url.Parse(state.RedirectURI)
		query := redirect.Query()
		query.Set("code", authorizationCode)
		if state.ClientState != "" {
			query.Set("state", state.ClientState)
		}
		redirect.RawQuery = query.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusFound)
		return
	}
	if session.Repository != "" && session.InstallationID <= 0 {
		installURL := "/auth/github/install?next=" + url.QueryEscape(state.Next)
		http.Redirect(w, r, installURL, http.StatusFound)
		return
	}
	http.Redirect(w, r, state.Next, http.StatusFound)
}

func (s *OAuthService) install(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	session, err := s.Authenticate(r)
	if err != nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	repository, ref, bindingErr := requestedBinding(r)
	if bindingErr != nil {
		http.Error(w, bindingErr.Error(), http.StatusBadRequest)
		return
	}
	if repository == "" {
		repository, ref = session.Repository, session.Ref
	}
	if repository == "" || ref == "" {
		http.Error(w, "repository and ref are required to install Code Relay", http.StatusBadRequest)
		return
	}
	session.Repository, session.Ref = repository, ref
	if err := s.setSession(w, session); err != nil {
		http.Error(w, "session unavailable", http.StatusInternalServerError)
		return
	}
	query := url.Values{"state": []string{safeNext(r.URL.Query().Get("next"))}}
	installURL := fmt.Sprintf("%s/apps/%s/installations/new?%s", strings.TrimRight(s.config.GitHubOAuthURL, "/"), url.PathEscape(s.config.AppSlug), query.Encode())
	http.Redirect(w, r, installURL, http.StatusFound)
}

func (s *OAuthService) appCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	session, err := s.Authenticate(r)
	if err != nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	installationID, err := strconv.ParseInt(r.URL.Query().Get("installation_id"), 10, 64)
	if err != nil || installationID <= 0 {
		http.Error(w, "invalid installation id", http.StatusBadRequest)
		return
	}
	if session.Repository == "" || session.Ref == "" {
		http.Error(w, "repository binding is required", http.StatusBadRequest)
		return
	}
	appClient := NewGitHubAppMembershipClient(s.config.GitHubAPIURL)
	resolvedID, err := appClient.FindUserInstallationForRepository(r.Context(), session.AccessToken, s.config.AppSlug, session.Repository)
	if err != nil || resolvedID != installationID {
		http.Error(w, "installation is not available to this user", http.StatusForbidden)
		return
	}
	session.InstallationID = installationID
	if err := s.setSession(w, session); err != nil {
		http.Error(w, "session unavailable", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, safeNext(r.URL.Query().Get("state")), http.StatusFound)
}

func (s *OAuthService) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if s.store != nil {
		if cookie, err := r.Cookie(s.config.SessionCookie); err == nil {
			_ = s.store.Revoke(r.Context(), cookie.Value)
		}
		if authorization := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
			_ = s.store.Revoke(r.Context(), strings.TrimSpace(authorization[len("bearer "):]))
		}
	}
	http.SetCookie(w, s.cookie(s.config.SessionCookie, "", -1))
	w.WriteHeader(http.StatusNoContent)
}

func (s *OAuthService) setSession(w http.ResponseWriter, session OAuthSession) error {
	if s.store != nil {
		ttl := time.Until(time.Unix(session.ExpiresAt, 0))
		id, err := s.store.Create(context.Background(), session, ttl)
		if err != nil {
			return err
		}
		http.SetCookie(w, s.cookie(s.config.SessionCookie, id, cookieMaxAge(ttl)))
		return nil
	}
	value, err := s.seal(session)
	if err != nil {
		return err
	}
	http.SetCookie(w, s.cookie(s.config.SessionCookie, value, int(time.Until(time.Unix(session.ExpiresAt, 0)).Seconds())))
	return nil
}

func cookieMaxAge(ttl time.Duration) int {
	seconds := int(ttl / time.Second)
	if ttl > 0 && seconds == 0 {
		return 1
	}
	return seconds
}

func (s *OAuthService) setBinding(w http.ResponseWriter, session OAuthSession, repository, ref string) error {
	normalizedRepository, normalizedRef, err := normalizeBinding(repository, ref)
	if err != nil {
		return err
	}
	session.Repository = normalizedRepository
	session.Ref = normalizedRef
	return s.setSession(w, session)
}

func (s *OAuthService) exchangeCode(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{"client_id": []string{s.config.ClientID}, "client_secret": []string{s.config.ClientSecret}, "code": []string{code}, "redirect_uri": []string{s.config.RedirectURL}, "code_verifier": []string{verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.config.GitHubOAuthURL, "/")+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("oauth exchange status %d", resp.StatusCode)
	}
	var token githubOAuthToken
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&token); err != nil {
		return "", err
	}
	if token.Error != "" {
		if token.ErrorDesc != "" {
			return "", fmt.Errorf("oauth provider error %s: %s", token.Error, token.ErrorDesc)
		}
		return "", fmt.Errorf("oauth provider error: %s", token.Error)
	}
	if token.AccessToken == "" {
		return "", errors.New("oauth provider returned no access token")
	}
	return token.AccessToken, nil
}

func (s *OAuthService) githubUser(ctx context.Context, token string) (githubUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.config.GitHubAPIURL, "/")+"/user", nil)
	if err != nil {
		return githubUser{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.client.Do(req)
	if err != nil {
		return githubUser{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return githubUser{}, fmt.Errorf("github user status %d", resp.StatusCode)
	}
	var user githubUser
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&user)
	return user, err
}

func (s *OAuthService) seal(value any) (string, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plaintext, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *OAuthService) open(value string, out any) error {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(data) < gcm.NonceSize() {
		return errors.New("invalid sealed value")
	}
	plaintext, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return errors.New("invalid sealed value")
	}
	return json.Unmarshal(plaintext, out)
}

func (s *OAuthService) cookie(name, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/", Domain: s.config.CookieDomain, MaxAge: maxAge, HttpOnly: true, Secure: s.config.SecureCookies, SameSite: http.SameSiteLaxMode}
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func safeNext(value string) string {
	if value == "" {
		return "/"
	}
	u, err := url.Parse(value)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "/"
	}
	return value
}

func requestedBinding(r *http.Request) (string, string, error) {
	repository := strings.TrimSpace(r.URL.Query().Get("repository"))
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	if repository == "" && ref == "" {
		return "", "", nil
	}
	if repository == "" || ref == "" {
		return "", "", errors.New("repository and ref must be provided together")
	}
	return normalizeBinding(repository, ref)
}

func normalizeBinding(repository, ref string) (string, string, error) {
	normalizedRepository, err := normalizeRepository(repository)
	if err != nil {
		return "", "", err
	}
	normalizedRef, err := normalizeRef(ref)
	if err != nil {
		return "", "", err
	}
	return normalizedRepository, normalizedRef, nil
}

func methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func safeOAuthError(err error) string {
	message := strings.Join(strings.Fields(err.Error()), " ")
	if len(message) > 256 {
		return message[:256]
	}
	return message
}
