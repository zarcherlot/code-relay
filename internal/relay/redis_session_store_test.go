package relay

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisSessionStoreIntegration(t *testing.T) {
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
	store, err := NewRedisSessionStore(client, "code-relay:test:")
	if err != nil {
		t.Fatal(err)
	}
	session := OAuthSession{Subject: "17", Login: "alice", AccessToken: "provider-token", ExpiresAt: time.Now().Add(time.Minute).Unix()}
	id, err := store.Create(ctx, session, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Del(context.Background(), store.key(id)).Err() })
	got, err := store.Get(ctx, id)
	if err != nil || got.Subject != session.Subject {
		t.Fatalf("get session: %+v, %v", got, err)
	}
	if err := store.Revoke(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, id); err != ErrSessionRevoked {
		t.Fatalf("revoked session error = %v", err)
	}
}

func TestRedisSessionRecordEncryption(t *testing.T) {
	store := &RedisSessionStore{encryptionKey: []byte("01234567890123456789012345678901")}
	record := redisSessionRecord{Session: OAuthSession{Subject: "17", AccessToken: "provider-secret"}}
	encoded, err := store.encode(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "provider-secret") {
		t.Fatal("redis session payload leaked provider token")
	}
	decoded, err := store.decode(encoded)
	if err != nil || decoded.Session.AccessToken != record.Session.AccessToken {
		t.Fatalf("decrypt session: %+v, %v", decoded, err)
	}
}
