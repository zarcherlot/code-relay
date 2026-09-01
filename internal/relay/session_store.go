package relay

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// SessionStore persists authenticated sessions without exposing provider
// credentials to a browser cookie. Implementations may be backed by Redis,
// SQL, or another shared store in a multi-instance deployment.
type SessionStore interface {
	Create(context.Context, OAuthSession, time.Duration) (string, error)
	Get(context.Context, string) (OAuthSession, error)
	Revoke(context.Context, string) error
	Rotate(context.Context, string, OAuthSession, time.Duration) (string, error)
}

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionRevoked  = errors.New("session revoked")
	ErrSessionExpired  = errors.New("session expired")
)

type memorySession struct {
	session OAuthSession
	expires time.Time
	revoked bool
}

// MemorySessionStore is a process-local reference implementation. It is
// suitable for tests and a single gateway instance, but should be replaced by
// a shared implementation before horizontally scaling the SaaS gateway.
type MemorySessionStore struct {
	mu       sync.RWMutex
	sessions map[string]memorySession
	now      func() time.Time
}

func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{sessions: make(map[string]memorySession), now: time.Now}
}

func (m *MemorySessionStore) Create(ctx context.Context, session OAuthSession, ttl time.Duration) (string, error) {
	if err := contextErr(ctx); err != nil {
		return "", err
	}
	if ttl <= 0 {
		return "", ErrSessionExpired
	}
	now := m.now()
	if session.ExpiresAt == 0 {
		expires := now.Add(ttl)
		session.ExpiresAt = expires.Unix()
		// OAuthSession uses second precision; ensure sub-second TTLs do not
		// become immediately expired while the record still has a live TTL.
		if session.ExpiresAt <= now.Unix() {
			session.ExpiresAt = now.Unix() + 1
		}
	}
	if session.ExpiresAt > 0 {
		ttl = minSessionTTL(ttl, time.Unix(session.ExpiresAt, 0).Sub(now))
	}
	if ttl <= 0 {
		return "", ErrSessionExpired
	}
	id, err := opaqueToken()
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[id] = memorySession{session: session, expires: m.now().Add(ttl)}
	return id, nil
}

func (m *MemorySessionStore) Get(ctx context.Context, id string) (OAuthSession, error) {
	if err := contextErr(ctx); err != nil {
		return OAuthSession{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.sessions[id]
	if !ok {
		return OAuthSession{}, ErrSessionNotFound
	}
	if record.revoked {
		return OAuthSession{}, ErrSessionRevoked
	}
	if !m.now().Before(record.expires) || record.session.ExpiresAt <= m.now().Unix() {
		delete(m.sessions, id)
		return OAuthSession{}, ErrSessionExpired
	}
	return record.session, nil
}

func (m *MemorySessionStore) Revoke(ctx context.Context, id string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	record.revoked = true
	m.sessions[id] = record
	return nil
}

func (m *MemorySessionStore) Rotate(ctx context.Context, id string, session OAuthSession, ttl time.Duration) (string, error) {
	if _, err := m.Get(ctx, id); err != nil {
		return "", err
	}
	if err := m.Revoke(ctx, id); err != nil {
		return "", err
	}
	return m.Create(ctx, session, ttl)
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func minSessionTTL(a, b time.Duration) time.Duration {
	if b < a {
		return b
	}
	return a
}

func opaqueToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
