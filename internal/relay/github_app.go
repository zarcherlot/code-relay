package relay

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type GitHubAppConfig struct {
	AppID         int64
	PrivateKeyPEM []byte
	APIBaseURL    string
}

type GitHubAppClient struct {
	appID      int64
	privateKey *rsa.PrivateKey
	baseURL    string
	client     *http.Client
}

type installationTokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type githubContent struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	SHA      string `json:"sha"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	Download string `json:"download_url"`
}

type githubRepo struct {
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
}

type githubInstallation struct {
	ID                  int64  `json:"id"`
	AppSlug             string `json:"app_slug"`
	RepositorySelection string `json:"repository_selection"`
}

// FindInstallationForUserRepository resolves the installation owned by the
// signed-in GitHub user and verifies that it grants this app access to the
// requested repository. It authenticates with the GitHub App JWT, avoiding the
// incompatible OAuth App user token used by /user/installations.
func (c *GitHubAppClient) FindInstallationForUserRepository(ctx context.Context, username, repository string) (int64, error) {
	if c == nil || c.privateKey == nil {
		return 0, errors.New("GitHub App signing key is not configured")
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return 0, errors.New("GitHub username is required")
	}
	repository, err := normalizeRepository(repository)
	if err != nil {
		return 0, err
	}
	jwt, err := c.appJWT()
	if err != nil {
		return 0, err
	}
	var installation githubInstallation
	if err := c.requestJSON(ctx, http.MethodGet, "/users/"+url.PathEscape(username)+"/installation", jwt, nil, &installation); err != nil {
		return 0, err
	}
	if installation.ID <= 0 {
		return 0, errInstallationNotFound
	}
	if err := c.verifyInstallationRepository(ctx, installation.ID, repository); err != nil {
		return 0, err
	}
	return installation.ID, nil
}

// VerifyInstallationForUserRepository checks that installationID belongs to
// the signed-in user for this App and grants access to repository.
func (c *GitHubAppClient) VerifyInstallationForUserRepository(ctx context.Context, username string, installationID int64, repository string) error {
	resolved, err := c.FindInstallationForUserRepository(ctx, username, repository)
	if err != nil {
		return err
	}
	if resolved != installationID {
		return errInstallationNotFound
	}
	return nil
}

func (c *GitHubAppClient) verifyInstallationRepository(ctx context.Context, installationID int64, repository string) error {
	parts := strings.SplitN(repository, "/", 2)
	if len(parts) != 2 {
		return errors.New("repository must be owner/name")
	}
	token, err := c.InstallationToken(ctx, installationID, []string{parts[1]})
	if err != nil {
		return err
	}
	var repo githubRepo
	if err := c.requestJSON(ctx, http.MethodGet, "/repos/"+repository, token.Token, nil, &repo); err != nil {
		return err
	}
	if !strings.EqualFold(repo.FullName, repository) {
		return errInstallationNotFound
	}
	return nil
}

var errInstallationNotFound = errors.New("GitHub App installation not found for repository")

func NewGitHubAppClient(config GitHubAppConfig) (*GitHubAppClient, error) {
	if config.AppID <= 0 {
		return nil, errors.New("GitHub App ID is required")
	}
	key, err := parsePrivateKey(config.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	if config.APIBaseURL == "" {
		config.APIBaseURL = defaultGitHubAPIURL
	}
	return &GitHubAppClient{appID: config.AppID, privateKey: key, baseURL: strings.TrimRight(config.APIBaseURL, "/"), client: &http.Client{Timeout: 20 * time.Second}}, nil
}

// NewGitHubAppMembershipClient creates a client that can verify a user's installation
// membership without requiring an app private key.  The full app client is
// still required for repository operations.
func NewGitHubAppMembershipClient(apiBaseURL string) *GitHubAppClient {
	if apiBaseURL == "" {
		apiBaseURL = defaultGitHubAPIURL
	}
	return &GitHubAppClient{baseURL: strings.TrimRight(apiBaseURL, "/"), client: &http.Client{Timeout: 15 * time.Second}}
}

func parsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("GitHub App private key is not PEM encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("unable to parse GitHub App private key")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("GitHub App private key must be RSA")
	}
	return key, nil
}

func (c *GitHubAppClient) appJWT() (string, error) {
	if c.appID <= 0 || c.privateKey == nil {
		return "", errors.New("GitHub App signing key is not configured")
	}
	now := time.Now().Unix()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, _ := json.Marshal(map[string]any{"iat": now - 60, "exp": now + appJWTLifetimeSeconds, "iss": c.appID})
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	message := header + "." + encodedPayload
	sum := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.privateKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return message + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

const appJWTLifetimeSeconds = 9 * 60

// InstallationToken exchanges the app JWT for a short-lived installation
// token.  The token is intentionally never returned in MCP responses.
func (c *GitHubAppClient) InstallationToken(ctx context.Context, installationID int64, repositories []string) (installationTokenResponse, error) {
	if installationID <= 0 {
		return installationTokenResponse{}, errors.New("invalid GitHub installation id")
	}
	jwt, err := c.appJWT()
	if err != nil {
		return installationTokenResponse{}, err
	}
	body := map[string]any{}
	if len(repositories) > 0 {
		body["repositories"] = repositories
	}
	data, _ := json.Marshal(body)
	path := fmt.Sprintf("/app/installations/%d/access_tokens", installationID)
	var out installationTokenResponse
	if err := c.requestJSON(ctx, http.MethodPost, path, jwt, data, &out); err != nil {
		return out, err
	}
	if out.Token == "" {
		return out, errors.New("GitHub App returned an empty installation token")
	}
	return out, nil
}

func (c *GitHubAppClient) VerifyUserInstallation(ctx context.Context, userToken string, installationID int64) error {
	_, err := c.listUserInstallationRepositories(ctx, userToken, installationID)
	return err
}

func (c *GitHubAppClient) listUserInstallationRepositories(ctx context.Context, userToken string, installationID int64) ([]githubRepo, error) {
	if strings.TrimSpace(userToken) == "" || installationID <= 0 {
		return nil, errors.New("invalid user token or installation id")
	}
	path := fmt.Sprintf("/user/installations/%d/repositories?per_page=100", installationID)
	var out struct {
		Repositories []githubRepo `json:"repositories"`
	}
	if err := c.requestJSON(ctx, http.MethodGet, path, userToken, nil, &out); err != nil {
		return nil, err
	}
	return out.Repositories, nil
}

func (c *GitHubAppClient) UserCanAccessRepository(ctx context.Context, userToken string, installationID int64, repository string) error {
	repositories, err := c.listUserInstallationRepositories(ctx, userToken, installationID)
	if err != nil {
		return err
	}
	for _, repo := range repositories {
		if strings.EqualFold(repo.FullName, repository) {
			return nil
		}
	}
	return fmt.Errorf("GitHub App installation does not grant access to %s", repository)
}

func (c *GitHubAppClient) FindUserInstallationForRepository(ctx context.Context, userToken, appSlug, repository string) (int64, error) {
	if strings.TrimSpace(appSlug) == "" {
		return 0, errors.New("GitHub App slug is required")
	}
	repository, err := normalizeRepository(repository)
	if err != nil {
		return 0, err
	}
	var out struct {
		Installations []githubInstallation `json:"installations"`
	}
	if err := c.requestJSON(ctx, http.MethodGet, "/user/installations?per_page=100", userToken, nil, &out); err != nil {
		return 0, err
	}
	for _, installation := range out.Installations {
		if installation.ID <= 0 || !strings.EqualFold(installation.AppSlug, appSlug) || installation.RepositorySelection == "all" {
			continue
		}
		repositories, err := c.listUserInstallationRepositories(ctx, userToken, installation.ID)
		if err != nil {
			return 0, err
		}
		for _, repo := range repositories {
			if strings.EqualFold(repo.FullName, repository) {
				return installation.ID, nil
			}
		}
	}
	return 0, errInstallationNotFound
}

func (c *GitHubAppClient) requestJSON(ctx context.Context, method, path, token string, body []byte, out any) error {
	if !strings.HasPrefix(path, "/") {
		return errors.New("GitHub API path must be relative")
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode/100 != 2 {
		var apiErr struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &apiErr)
		if apiErr.Message == "" {
			apiErr.Message = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("GitHub API %s: %s", resp.Status, apiErr.Message)
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

type GitHubRepositoryClient struct {
	app   *GitHubAppClient
	token string
}

func (c *GitHubAppClient) Repository(ctx context.Context, installationID int64, repository string) (*GitHubRepositoryClient, error) {
	repository, err := normalizeRepository(repository)
	if err != nil {
		return nil, err
	}
	// The installation-token API expects repository names (not owner/name).
	parts := strings.SplitN(repository, "/", 2)
	token, err := c.InstallationToken(ctx, installationID, []string{parts[1]})
	if err != nil {
		return nil, err
	}
	return &GitHubRepositoryClient{app: c, token: token.Token}, nil
}

func (c *GitHubRepositoryClient) GetContent(ctx context.Context, repository, path, ref string) ([]byte, string, error) {
	path, err := safeRepoPath(path)
	if err != nil {
		return nil, "", err
	}
	query := url.Values{"ref": []string{ref}}.Encode()
	var out githubContent
	if err := c.app.requestJSON(ctx, http.MethodGet, "/repos/"+repository+"/contents/"+path+"?"+query, c.token, nil, &out); err != nil {
		return nil, "", err
	}
	if out.Type != "file" {
		return nil, "", errors.New("GitHub content is not a file")
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
	return data, out.SHA, err
}

func (c *GitHubRepositoryClient) PutContent(ctx context.Context, repository, path, ref, message string, content []byte, sha string) error {
	path, err := safeRepoPath(path)
	if err != nil {
		return err
	}
	body := map[string]any{"message": message, "content": base64.StdEncoding.EncodeToString(content), "branch": strings.TrimPrefix(ref, "refs/heads/")}
	if sha != "" {
		body["sha"] = sha
	}
	data, _ := json.Marshal(body)
	return c.app.requestJSON(ctx, http.MethodPut, "/repos/"+repository+"/contents/"+path, c.token, data, nil)
}

func (c *GitHubRepositoryClient) DispatchWorkflow(ctx context.Context, repository, workflow, ref string, inputs map[string]string) error {
	workflow = strings.TrimSpace(workflow)
	if workflow == "" || strings.ContainsAny(workflow, `/\\`) || !strings.HasSuffix(workflow, ".yml") && !strings.HasSuffix(workflow, ".yaml") {
		return errors.New("workflow must be a .yml or .yaml filename")
	}
	body := map[string]any{"ref": strings.TrimPrefix(ref, "refs/heads/")}
	if len(inputs) > 0 {
		body["inputs"] = inputs
	}
	data, _ := json.Marshal(body)
	return c.app.requestJSON(ctx, http.MethodPost, "/repos/"+repository+"/actions/workflows/"+url.PathEscape(workflow)+"/dispatches", c.token, data, nil)
}

func (c *GitHubRepositoryClient) GetRef(ctx context.Context, repository, ref string) error {
	branch := strings.TrimPrefix(ref, "refs/heads/")
	_, err := c.app.requestJSONRaw(ctx, http.MethodGet, "/repos/"+repository+"/git/ref/heads/"+url.PathEscape(branch), c.token)
	return err
}

func (c *GitHubRepositoryClient) ListDirectory(ctx context.Context, repository, path, ref string) ([]githubContent, error) {
	path, err := safeRepoPath(path)
	if err != nil {
		return nil, err
	}
	query := url.Values{"ref": []string{ref}}.Encode()
	var out []githubContent
	if err := c.app.requestJSON(ctx, http.MethodGet, "/repos/"+repository+"/contents/"+path+"?"+query, c.token, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *GitHubAppClient) requestJSONRaw(ctx context.Context, method, path, token string) ([]byte, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, errors.New("GitHub API path must be relative")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		var apiErr struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &apiErr)
		if apiErr.Message == "" {
			apiErr.Message = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("GitHub API %s: %s", resp.Status, apiErr.Message)
	}
	return data, nil
}

func normalizeRepository(value string) (string, error) {
	if strings.ContainsAny(value, "\\\r\n \t") {
		return "", errors.New("repository contains unsupported characters")
	}
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "https://github.com/") {
		value = strings.TrimPrefix(value, "https://github.com/")
	}
	value = strings.TrimSuffix(value, ".git")
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", errors.New("repository must be owner/name")
	}
	for _, part := range parts {
		for _, r := range part {
			if !(r == '-' || r == '_' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
				return "", errors.New("repository contains unsupported characters")
			}
		}
	}
	return parts[0] + "/" + parts[1], nil
}

func safeRepoPath(path string) (string, error) {
	path = strings.Trim(path, "/")
	if path == "" || strings.Contains(path, "..") || strings.ContainsAny(path, "\\\r\n") {
		return "", errors.New("invalid repository path")
	}
	return path, nil
}
