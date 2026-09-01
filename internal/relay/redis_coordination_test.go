package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisCoordinationIntegration(t *testing.T) {
	redisURL := strings.TrimSpace(os.Getenv("CODE_RELAY_REDIS_URL"))
	if redisURL == "" {
		t.Skip("CODE_RELAY_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}
	prefix := "code-relay:test:" + strings.ReplaceAll(t.Name(), "/", "-") + ":" + time.Now().Format("150405.000000000") + ":"
	codeStore, err := NewRedisAuthorizationCodeStore(client, prefix, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	record := OAuthAuthorizationCode{ClientID: "client", RedirectURI: "https://client.example/cb", Challenge: "challenge", ExpiresAt: time.Now().Add(time.Minute)}
	if err := codeStore.Put(ctx, "code-1", record, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := codeStore.Consume(ctx, "code-1")
	if err != nil || got.ClientID != record.ClientID {
		t.Fatalf("consume code = %+v, %v", got, err)
	}
	if _, err := codeStore.Consume(ctx, "code-1"); err == nil {
		t.Fatal("authorization code was reusable")
	}
	sessionStore, err := NewRedisSessionStoreWithSecret(client, prefix, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	serviceA, err := NewOAuthService(OAuthConfig{ClientID: "github", ClientSecret: "secret", RedirectURL: "https://relay.example/callback", SessionSecret: strings.Repeat("s", 32), AppSlug: "code-relay", SessionStore: sessionStore, AuthorizationClientID: "mcp-client", AuthorizationRedirectURLs: []string{"https://client.example/cb"}, AuthorizationCodeStore: codeStore})
	if err != nil {
		t.Fatal(err)
	}
	serviceB, err := NewOAuthService(OAuthConfig{ClientID: "github", ClientSecret: "secret", RedirectURL: "https://relay.example/callback", SessionSecret: strings.Repeat("s", 32), AppSlug: "code-relay", SessionStore: sessionStore, AuthorizationClientID: "mcp-client", AuthorizationRedirectURLs: []string{"https://client.example/cb"}, AuthorizationCodeStore: codeStore})
	if err != nil {
		t.Fatal(err)
	}
	authSession := OAuthSession{Subject: "17", Login: "alice", AccessToken: "github-token", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	code, err := serviceA.issueAuthorizationCodeContext(ctx, authSession, oauthState{ClientID: "mcp-client", RedirectURI: "https://client.example/cb", CodeChallenge: pkceChallenge("verifier-abcdefghijklmnopqrstuvwxyz")})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {"mcp-client"}, "redirect_uri": {"https://client.example/cb"}, "code": {code}, "code_verifier": {"verifier-abcdefghijklmnopqrstuvwxyz"}}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	serviceB.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("cross-instance OAuth token status = %d, body=%s", res.Code, res.Body.String())
	}

	registry, err := NewRedisMCPSessionRegistry(client, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(ctx, "session-1", "tenant-1", time.Minute); err != nil {
		t.Fatal(err)
	}
	if owner, err := registry.Owner(ctx, "session-1"); err != nil || owner != "tenant-1" {
		t.Fatalf("session owner = %q, %v", owner, err)
	}

	limiter, err := NewRedisRateLimiter(client, prefix)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := limiter.Allow(ctx, "tenant-1", 1, time.Minute)
	if err != nil || !allowed {
		t.Fatalf("first rate-limit decision = %v, %v", allowed, err)
	}
	allowed, err = limiter.Allow(ctx, "tenant-1", 1, time.Minute)
	if err != nil || allowed {
		t.Fatalf("second rate-limit decision = %v, %v", allowed, err)
	}

	lock, err := NewRedisDistributedLock(client, prefix)
	if err != nil {
		t.Fatal(err)
	}
	release, err := lock.Acquire(ctx, "run-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Acquire(ctx, "run-1", time.Minute); err == nil {
		t.Fatal("distributed lock was not exclusive")
	}
	if err := release(ctx); err != nil {
		t.Fatal(err)
	}
}
