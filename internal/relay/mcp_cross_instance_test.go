package relay

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestMCPRedisCrossInstanceSSE(t *testing.T) {
	redisURL := strings.TrimSpace(os.Getenv("CODE_RELAY_REDIS_URL"))
	if redisURL == "" {
		t.Skip("CODE_RELAY_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	producerClient := redis.NewClient(options)
	consumerClient := redis.NewClient(options)
	t.Cleanup(func() { _ = producerClient.Close(); _ = consumerClient.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := producerClient.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}
	producer, err := NewRedisSessionEventStore(producerClient, "code-relay:test:")
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := NewRedisSessionEventStore(consumerClient, "code-relay:test:")
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRedisMCPSessionRegistry(producerClient, "code-relay:test:")
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("t", 32)
	handlerA, err := MCPHTTPHandler(MCPHTTPConfig{Root: t.TempDir(), BearerToken: token, EventStore: producer, SessionRegistry: registry})
	if err != nil {
		t.Fatal(err)
	}
	handlerB, err := MCPHTTPHandler(MCPHTTPConfig{Root: t.TempDir(), BearerToken: token, EventStore: consumer, SessionRegistry: registry})
	if err != nil {
		t.Fatal(err)
	}
	initReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)))
	initReq.RemoteAddr = "127.0.0.1:1234"
	initReq.Header.Set("Authorization", "Bearer "+token)
	initReq.Header.Set("Content-Type", "application/json")
	initRes := httptest.NewRecorder()
	handlerA.ServeHTTP(initRes, initReq)
	sessionID := initRes.Header().Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("missing session id")
	}

	server := httptest.NewServer(handlerB)
	t.Cleanup(server.Close)
	readDone := make(chan error, 1)
	go func() {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/mcp", nil)
		if err != nil {
			readDone <- err
			return
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Accept", "text/event-stream")
		request.Header.Set("Mcp-Session-Id", sessionID)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			readDone <- err
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			readDone <- fmt.Errorf("status = %d", response.StatusCode)
			return
		}
		reader := bufio.NewReader(response.Body)
		for {
			line, readErr := reader.ReadString('\n')
			if strings.Contains(line, "cross-instance") {
				readDone <- nil
				return
			}
			if readErr != nil {
				readDone <- readErr
				return
			}
		}
	}()
	time.Sleep(100 * time.Millisecond)
	if _, err := producer.Publish(ctx, sessionID, []byte(`{"jsonrpc":"2.0","method":"notifications/message","params":{"data":"cross-instance"}}`), 256); err != nil {
		t.Fatal(err)
	}
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
}
