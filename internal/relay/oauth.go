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
	State      string `json:"state"`
	Verifier   string `json:"verifier"`
	Next       string `json:"next"`
	Repository string `json:"repository,omitempty"`
	Ref        string `json:"ref,omitempty"`
	Expires    int64  `json:"exp"`
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
}

func NewOAuthService(config OAuthConfig) (*OAuthService, error) {
	config.ClientID = strings.TrimSpace(config.ClientID)
	config.ClientSecret = strings.TrimSpace(config.ClientSecret)
	config.RedirectURL = strings.TrimSpace(config.RedirectURL)
	config.SessionSecret = strings.TrimSpace(config.SessionSecret)
	config.AppSlug = strings.TrimSpace(config.AppSlug)
	config.GitHubOAuthURL = strings.TrimSpace(config.GitHubOAuthURL)
	config.GitHubAPIURL = strings.TrimSpace(config.GitHubAPIURL)
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
	return &OAuthService{config: config, client: &http.Client{Timeout: 15 * time.Second}, key: key[:]}, nil
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
	default:
		return false
	}
}

func (s *OAuthService) Authenticate(r *http.Request) (OAuthSession, error) {
	cookie, err := r.Cookie(s.config.SessionCookie)
	if err != nil {
		return OAuthSession{}, errors.New("authentication required")
	}
	var session OAuthSession
	if err := s.open(cookie.Value, &session); err != nil {
		return OAuthSession{}, errors.New("invalid session")
	}
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
	http.SetCookie(w, s.cookie(s.config.SessionCookie, "", -1))
	w.WriteHeader(http.StatusNoContent)
}

func (s *OAuthService) setSession(w http.ResponseWriter, session OAuthSession) error {
	value, err := s.seal(session)
	if err != nil {
		return err
	}
	http.SetCookie(w, s.cookie(s.config.SessionCookie, value, int(time.Until(time.Unix(session.ExpiresAt, 0)).Seconds())))
	return nil
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
