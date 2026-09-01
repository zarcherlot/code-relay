package relay

import (
	"encoding/json"
	"net/http"
	"strings"
)

// protectedResourceMetadata is the OAuth Protected Resource Metadata shape
// used by MCP clients to discover the authorization server for /mcp.
type protectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers,omitempty"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
}

type authorizationServerMetadata struct {
	Issuer                        string   `json:"issuer,omitempty"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint,omitempty"`
	TokenEndpoint                 string   `json:"token_endpoint,omitempty"`
	ScopesSupported               []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported        []string `json:"response_types_supported,omitempty"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported,omitempty"`
	GrantTypesSupported           []string `json:"grant_types_supported,omitempty"`
	TokenEndpointAuthMethods      []string `json:"token_endpoint_auth_methods_supported,omitempty"`
}

func (s *OAuthService) metadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasSuffix(r.URL.Path, "oauth-protected-resource") {
		resource := s.config.ResourceURL
		if resource == "" {
			scheme := "https"
			if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "" {
				scheme = r.Header.Get("X-Forwarded-Proto")
			}
			resource = scheme + "://" + r.Host + "/mcp"
		}
		metadata := protectedResourceMetadata{Resource: resource, BearerMethodsSupported: []string{"header"}, ScopesSupported: append([]string(nil), s.config.OAuthScopes...)}
		if s.config.AuthorizationServerURL != "" {
			metadata.AuthorizationServers = []string{s.config.AuthorizationServerURL}
		} else if s.config.IssuerURL != "" {
			metadata.AuthorizationServers = []string{s.config.IssuerURL}
		}
		_ = json.NewEncoder(w).Encode(metadata)
		return
	}
	metadata := authorizationServerMetadata{Issuer: s.config.IssuerURL, ScopesSupported: append([]string(nil), s.config.OAuthScopes...), ResponseTypesSupported: []string{"code"}, CodeChallengeMethodsSupported: []string{"S256"}}
	base := strings.TrimRight(s.config.IssuerURL, "/")
	if base != "" {
		if s.config.AuthorizationClientID != "" {
			metadata.AuthorizationEndpoint = base + "/oauth/authorize"
			metadata.GrantTypesSupported = []string{"authorization_code"}
			metadata.TokenEndpointAuthMethods = []string{"none"}
			if endpoint := strings.TrimSpace(s.config.TokenEndpointURL); endpoint != "" {
				metadata.TokenEndpoint = endpoint
			} else {
				metadata.TokenEndpoint = base + "/oauth/token"
			}
		} else {
			metadata.AuthorizationEndpoint = base + "/auth/github"
		}
	}
	_ = json.NewEncoder(w).Encode(metadata)
}
