package relay

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisEventStoreIntegration(t *testing.T) {
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
	store, err := NewRedisSessionEventStore(client, "code-relay:test:")
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "event-" + time.Now().UTC().Format("20060102150405.000000000")
	t.Cleanup(func() { _ = client.Del(context.Background(), store.key(sessionID)).Err() })
	first, err := store.Publish(ctx, sessionID, []byte(`{"n":1}`), 256)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" {
		t.Fatal("empty Redis stream id")
	}
	second, err := store.Publish(ctx, sessionID, []byte(`{"n":2}`), 256)
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.Read(ctx, sessionID, first, time.Second)
	if err != nil || len(events) != 1 || events[0].ID != second || string(events[0].Data) != `{"n":2}` {
		t.Fatalf("read events: %+v, %v", events, err)
	}
}
