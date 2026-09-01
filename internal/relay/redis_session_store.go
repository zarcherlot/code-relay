package relay

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisSessionStore is the shared SessionStore implementation for a
// multi-instance gateway. Redis TTL is the primary expiry mechanism; the
// OAuthSession expiry is checked as a second defense on reads.
type RedisSessionStore struct {
	client        redis.UniversalClient
	prefix        string
	encryptionKey []byte
}

func NewRedisSessionStore(client redis.UniversalClient, prefix string) (*RedisSessionStore, error) {
	return NewRedisSessionStoreWithSecret(client, prefix, "")
}

func NewRedisSessionStoreWithSecret(client redis.UniversalClient, prefix, secret string) (*RedisSessionStore, error) {
	if client == nil {
		return nil, ErrRedisClientRequired
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "code-relay:"
	}
	var key []byte
	if strings.TrimSpace(secret) != "" {
		digest := sha256.Sum256([]byte(secret))
		key = digest[:]
	}
	return &RedisSessionStore{client: client, prefix: prefix, encryptionKey: key}, nil
}

var ErrRedisClientRequired = redisError("redis client is required")

type redisError string

func (e redisError) Error() string { return string(e) }

type redisSessionRecord struct {
	Session OAuthSession `json:"session"`
	Revoked bool         `json:"revoked"`
}

func (s *RedisSessionStore) key(id string) string {
	return s.prefix + "session:" + id
}

func (s *RedisSessionStore) Create(ctx context.Context, session OAuthSession, ttl time.Duration) (string, error) {
	if err := contextErr(ctx); err != nil {
		return "", err
	}
	if ttl <= 0 {
		return "", ErrSessionExpired
	}
	now := time.Now()
	if session.ExpiresAt == 0 {
		session.ExpiresAt = now.Add(ttl).Unix()
	}
	if expiry := time.Until(time.Unix(session.ExpiresAt, 0)); expiry < ttl {
		ttl = expiry
	}
	if ttl <= 0 {
		return "", ErrSessionExpired
	}
	id, err := opaqueToken()
	if err != nil {
		return "", err
	}
	data, err := s.encode(redisSessionRecord{Session: session})
	if err != nil {
		return "", err
	}
	if err := s.client.Set(ctx, s.key(id), data, ttl).Err(); err != nil {
		return "", err
	}
	return id, nil
}

func (s *RedisSessionStore) Get(ctx context.Context, id string) (OAuthSession, error) {
	if err := contextErr(ctx); err != nil {
		return OAuthSession{}, err
	}
	value, err := s.client.Get(ctx, s.key(strings.TrimSpace(id))).Bytes()
	if err == redis.Nil {
		return OAuthSession{}, ErrSessionNotFound
	}
	if err != nil {
		return OAuthSession{}, err
	}
	record, err := s.decode(value)
	if err != nil {
		return OAuthSession{}, err
	}
	if record.Revoked {
		return OAuthSession{}, ErrSessionRevoked
	}
	if record.Session.ExpiresAt <= time.Now().Unix() {
		return OAuthSession{}, ErrSessionExpired
	}
	return record.Session, nil
}

func (s *RedisSessionStore) Revoke(ctx context.Context, id string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	key := s.key(strings.TrimSpace(id))
	value, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return ErrSessionNotFound
	}
	if err != nil {
		return err
	}
	record, err := s.decode(value)
	if err != nil {
		return err
	}
	record.Revoked = true
	data, err := s.encode(record)
	if err != nil {
		return err
	}
	ttl, err := s.client.TTL(ctx, key).Result()
	if err != nil {
		return err
	}
	if ttl <= 0 {
		return ErrSessionExpired
	}
	return s.client.Set(ctx, key, data, ttl).Err()
}

func (s *RedisSessionStore) encode(record redisSessionRecord) ([]byte, error) {
	plaintext, err := json.Marshal(record)
	if err != nil || len(s.encryptionKey) == 0 {
		return plaintext, err
	}
	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	out := make([]byte, base64.RawURLEncoding.EncodedLen(len(sealed)))
	base64.RawURLEncoding.Encode(out, sealed)
	return out, nil
}

func (s *RedisSessionStore) decode(value []byte) (redisSessionRecord, error) {
	if len(s.encryptionKey) == 0 {
		var record redisSessionRecord
		return record, json.Unmarshal(value, &record)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(string(value))
	if err != nil {
		return redisSessionRecord{}, err
	}
	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return redisSessionRecord{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(decoded) < gcm.NonceSize() {
		if err == nil {
			err = errors.New("invalid encrypted redis session")
		}
		return redisSessionRecord{}, err
	}
	plaintext, err := gcm.Open(nil, decoded[:gcm.NonceSize()], decoded[gcm.NonceSize():], nil)
	if err != nil {
		return redisSessionRecord{}, err
	}
	var record redisSessionRecord
	if err := json.Unmarshal(plaintext, &record); err != nil {
		return redisSessionRecord{}, err
	}
	return record, nil
}

func (s *RedisSessionStore) Rotate(ctx context.Context, id string, session OAuthSession, ttl time.Duration) (string, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return "", err
	}
	if err := s.Revoke(ctx, id); err != nil {
		return "", err
	}
	return s.Create(ctx, session, ttl)
}
