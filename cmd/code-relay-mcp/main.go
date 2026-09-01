package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zarcherlot/code-relay/internal/relay"
)

var version = "3.1.0"

func main() {
	relay.SetVersion(version)
	addr := flag.String("addr", envOr("PORT", "8080"), "HTTP listen address or port")
	root := flag.String("root", os.Getenv("CODE_RELAY_MCP_ROOT"), "fixed project root")
	flag.Parse()
	if !strings.Contains(*addr, ":") {
		*addr = ":" + *addr
	}
	var absRoot string
	var err error
	if strings.TrimSpace(*root) != "" {
		absRoot, err = filepath.Abs(*root)
		if err != nil {
			fatal("resolve MCP root: %v", err)
		}
	}
	config := relay.MCPHTTPConfig{
		Root:              absRoot,
		BearerToken:       os.Getenv("CODE_RELAY_MCP_TOKEN"),
		DomainChallenge:   os.Getenv("OPENAI_APPS_CHALLENGE"),
		RatePerMinute:     envInt("CODE_RELAY_MCP_RATE_PER_MINUTE", 60),
		MaxConcurrent:     envInt("CODE_RELAY_MCP_MAX_CONCURRENT", 4),
		RequestTimeout:    time.Duration(envInt("CODE_RELAY_MCP_TIMEOUT_SECONDS", 60)) * time.Second,
		AllowedOrigins:    splitCSV(os.Getenv("CODE_RELAY_CORS_ALLOWED_ORIGINS")),
		SessionTTL:        envDuration("CODE_RELAY_SESSION_TTL", 30*time.Minute),
		SSEHeartbeat:      time.Duration(envInt("CODE_RELAY_SSE_HEARTBEAT_SECONDS", 15)) * time.Second,
		SSEMaxQueue:       envInt("CODE_RELAY_SSE_MAX_QUEUE", 32),
		SSEEventHistory:   envInt("CODE_RELAY_SSE_EVENT_HISTORY", 256),
		MaxSSEConnections: envInt("CODE_RELAY_SSE_MAX_CONNECTIONS", 1000),
	}
	var controlPlane *relay.PostgresControlPlane
	remoteEnabled := strings.TrimSpace(os.Getenv("CODE_RELAY_GITHUB_OAUTH_CLIENT_ID")) != ""
	if remoteEnabled {
		appID, parseErr := strconv.ParseInt(strings.TrimSpace(os.Getenv("CODE_RELAY_GITHUB_APP_ID")), 10, 64)
		if parseErr != nil || appID <= 0 {
			fatal("CODE_RELAY_GITHUB_APP_ID must be a positive integer")
		}
		privateKey := []byte(os.Getenv("CODE_RELAY_GITHUB_APP_PRIVATE_KEY"))
		if keyFile := strings.TrimSpace(os.Getenv("CODE_RELAY_GITHUB_APP_PRIVATE_KEY_FILE")); keyFile != "" {
			privateKey, err = os.ReadFile(keyFile)
			if err != nil {
				fatal("read GitHub App private key: %v", err)
			}
		}
		privateKey = []byte(strings.ReplaceAll(string(privateKey), `\n`, "\n"))
		apiURL := envOr("CODE_RELAY_GITHUB_API_URL", "https://api.github.com")
		app, appErr := relay.NewGitHubAppClient(relay.GitHubAppConfig{AppID: appID, PrivateKeyPEM: privateKey, APIBaseURL: apiURL})
		if appErr != nil {
			fatal("configure GitHub App: %v", appErr)
		}
		backend, backendErr := relay.NewGitHubRemoteBackend(app, envOr("CODE_RELAY_GITHUB_WORKFLOW", "checkpoint.yml"))
		if backendErr != nil {
			fatal("configure remote backend: %v", backendErr)
		}
		backend.SetAllowedRefs(splitCSV(os.Getenv("CODE_RELAY_ALLOWED_REFS")))
		if dsn := strings.TrimSpace(os.Getenv("CODE_RELAY_DATABASE_URL")); dsn != "" {
			controlPlane, err = relay.NewPostgresControlPlane(context.Background(), dsn)
			if err != nil {
				fatal("connect to PostgreSQL control plane: %v", err)
			}
			defer controlPlane.Close()
			config.ControlPlane = controlPlane
		}
		oauthClientSecret, secretErr := envSecret("CODE_RELAY_GITHUB_OAUTH_CLIENT_SECRET", "CODE_RELAY_GITHUB_OAUTH_CLIENT_SECRET_FILE")
		if secretErr != nil {
			fatal("configure GitHub OAuth client secret: %v", secretErr)
		}
		publicBaseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("CODE_RELAY_PUBLIC_BASE_URL")), "/")
		issuerURL := strings.TrimRight(strings.TrimSpace(os.Getenv("CODE_RELAY_OAUTH_ISSUER_URL")), "/")
		if issuerURL == "" {
			issuerURL = publicBaseURL
		}
		resourceURL := strings.TrimRight(strings.TrimSpace(os.Getenv("CODE_RELAY_OAUTH_RESOURCE_URL")), "/")
		if resourceURL == "" && publicBaseURL != "" {
			resourceURL = publicBaseURL + "/mcp"
		}
		oauthConfig := relay.OAuthConfig{
			ClientID:                  os.Getenv("CODE_RELAY_GITHUB_OAUTH_CLIENT_ID"),
			ClientSecret:              oauthClientSecret,
			RedirectURL:               os.Getenv("CODE_RELAY_GITHUB_OAUTH_REDIRECT_URL"),
			SessionSecret:             os.Getenv("CODE_RELAY_SESSION_SECRET"),
			AppSlug:                   os.Getenv("CODE_RELAY_GITHUB_APP_SLUG"),
			GitHubOAuthURL:            envOr("CODE_RELAY_GITHUB_OAUTH_URL", "https://github.com"),
			GitHubAPIURL:              apiURL,
			CookieDomain:              os.Getenv("CODE_RELAY_COOKIE_DOMAIN"),
			SecureCookies:             envBool("CODE_RELAY_COOKIE_SECURE", true),
			IssuerURL:                 issuerURL,
			ResourceURL:               resourceURL,
			OAuthScopes:               splitCSV(os.Getenv("CODE_RELAY_OAUTH_SCOPES")),
			AuthorizationClientID:     strings.TrimSpace(os.Getenv("CODE_RELAY_MCP_OAUTH_CLIENT_ID")),
			AuthorizationRedirectURLs: splitCSV(os.Getenv("CODE_RELAY_MCP_OAUTH_REDIRECT_URIS")),
			AuthorizationCodeTTL:      envDuration("CODE_RELAY_OAUTH_CODE_TTL", time.Minute),
		}
		// Memory sessions make the hosted single-instance deployment use opaque
		// browser cookies and bearer credentials. Set CODE_RELAY_SESSION_STORE=cookie
		// only for compatibility testing; replace this store with Redis before
		// running more than one gateway instance.
		if strings.EqualFold(envOr("CODE_RELAY_SESSION_STORE", "memory"), "memory") {
			oauthConfig.SessionStore = relay.NewMemorySessionStore()
		} else if strings.EqualFold(envOr("CODE_RELAY_SESSION_STORE", "memory"), "redis") {
			redisURL := strings.TrimSpace(os.Getenv("CODE_RELAY_REDIS_URL"))
			options, parseErr := redis.ParseURL(redisURL)
			if parseErr != nil {
				fatal("parse CODE_RELAY_REDIS_URL: %v", parseErr)
			}
			redisClient := redis.NewClient(options)
			pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
			pingErr := redisClient.Ping(pingCtx).Err()
			cancelPing()
			if pingErr != nil {
				_ = redisClient.Close()
				fatal("connect to Redis: %v", pingErr)
			}
			store, storeErr := relay.NewRedisSessionStoreWithSecret(redisClient, os.Getenv("CODE_RELAY_REDIS_KEY_PREFIX"), os.Getenv("CODE_RELAY_SESSION_SECRET"))
			if storeErr != nil {
				_ = redisClient.Close()
				fatal("configure Redis session store: %v", storeErr)
			}
			oauthConfig.SessionStore = store
			eventStore, eventErr := relay.NewRedisSessionEventStore(redisClient, os.Getenv("CODE_RELAY_REDIS_KEY_PREFIX"))
			if eventErr != nil {
				_ = redisClient.Close()
				fatal("configure Redis event store: %v", eventErr)
			}
			config.EventStore = eventStore
			defer redisClient.Close()
		} else if !strings.EqualFold(envOr("CODE_RELAY_SESSION_STORE", "memory"), "cookie") {
			fatal("CODE_RELAY_SESSION_STORE must be memory, redis, or cookie")
		}
		oauth, oauthErr := relay.NewOAuthService(oauthConfig)
		if oauthErr != nil {
			fatal("configure OAuth: %v", oauthErr)
		}
		config.OAuth = oauth
		config.RemoteBackend = backend
	} else if strings.TrimSpace(absRoot) == "" {
		fatal("CODE_RELAY_MCP_ROOT is required in staging mode; configure CODE_RELAY_GITHUB_OAUTH_CLIENT_ID for hosted mode")
	}
	handler, err := relay.MCPHTTPHandler(config)
	if err != nil {
		fatal("configure MCP gateway: %v", err)
	}
	// WriteTimeout must remain disabled for the long-lived Streamable HTTP GET
	// event stream. Request-level deadlines are enforced by MCPHTTPConfig.
	server := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 70 * time.Second, WriteTimeout: 0, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		if drainable, ok := handler.(interface {
			BeginDrain()
			WaitForDrain(context.Context) error
		}); ok {
			drainable.BeginDrain()
			drainCtx, cancelDrain := context.WithTimeout(context.Background(), 8*time.Second)
			_ = drainable.WaitForDrain(drainCtx)
			cancelDrain()
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if config.RemoteBackend != nil {
		fmt.Printf("Code Relay MCP gateway listening on %s (hosted GitHub App mode)\n", *addr)
	} else {
		fmt.Printf("Code Relay MCP gateway listening on %s (root=%s)\n", *addr, absRoot)
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal("MCP gateway: %v", err)
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func envSecret(valueName, fileName string) (string, error) {
	if value := strings.TrimSpace(os.Getenv(valueName)); value != "" {
		return value, nil
	}
	path := strings.TrimSpace(os.Getenv(fileName))
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", fileName, err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("%s is empty", fileName)
	}
	return value, nil
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
