package relay

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemorySessionStoreLifecycle(t *testing.T) {
	store := NewMemorySessionStore()
	session := OAuthSession{Subject: "17", AccessToken: "github-token", ExpiresAt: time.Now().Add(time.Minute).Unix()}
	id, err := store.Create(context.Background(), session, time.Minute)
	if err != nil || id == "" {
		t.Fatalf("create: %q, %v", id, err)
	}
	if id == session.AccessToken {
		t.Fatal("store returned provider token")
	}
	got, err := store.Get(context.Background(), id)
	if err != nil || got.Subject != session.Subject {
		t.Fatalf("get: %+v, %v", got, err)
	}
	rotated, err := store.Rotate(context.Background(), id, session, time.Minute)
	if err != nil || rotated == id {
		t.Fatalf("rotate: %q, %v", rotated, err)
	}
	if _, err := store.Get(context.Background(), id); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("old session should be revoked, got %v", err)
	}
	if err := store.Revoke(context.Background(), rotated); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), rotated); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("revoked session, got %v", err)
	}
}

func TestMemorySessionStoreExpiry(t *testing.T) {
	store := NewMemorySessionStore()
	id, err := store.Create(context.Background(), OAuthSession{Subject: "17", AccessToken: "token"}, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := store.Get(context.Background(), id); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expected expiry, got %v", err)
	}
}
