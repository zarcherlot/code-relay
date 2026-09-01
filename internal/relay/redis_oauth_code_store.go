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

// RedisAuthorizationCodeStore is a shared, encrypted and one-time OAuth
// authorization-code store. GetDel provides the atomic consume semantics that
// prevent a code from being redeemed by two gateway instances.
type RedisAuthorizationCodeStore struct {
	client redis.UniversalClient
	prefix string
	key    []byte
}

func NewRedisAuthorizationCodeStore(client redis.UniversalClient, prefix, secret string) (*RedisAuthorizationCodeStore, error) {
	if client == nil {
		return nil, ErrRedisClientRequired
	}
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("OAuth authorization-code encryption secret is required")
	}
	digest := sha256.Sum256([]byte(secret))
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "code-relay:"
	}
	return &RedisAuthorizationCodeStore{client: client, prefix: prefix, key: digest[:]}, nil
}

func (s *RedisAuthorizationCodeStore) redisKey(code string) string {
	return s.prefix + "oauth-code:" + strings.TrimSpace(code)
}

func (s *RedisAuthorizationCodeStore) Put(ctx context.Context, code string, record OAuthAuthorizationCode, ttl time.Duration) error {
	if ttl <= 0 {
		return ErrSessionExpired
	}
	value, err := s.seal(record)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, s.redisKey(code), value, ttl).Err()
}

func (s *RedisAuthorizationCodeStore) Consume(ctx context.Context, code string) (OAuthAuthorizationCode, error) {
	value, err := s.client.GetDel(ctx, s.redisKey(code)).Bytes()
	if err == redis.Nil {
		return OAuthAuthorizationCode{}, errors.New("authorization code not found")
	}
	if err != nil {
		return OAuthAuthorizationCode{}, err
	}
	var record OAuthAuthorizationCode
	if err := s.open(value, &record); err != nil {
		return OAuthAuthorizationCode{}, err
	}
	return record, nil
}

func (s *RedisAuthorizationCodeStore) seal(value any) ([]byte, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plaintext, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	encoded := make([]byte, base64.RawURLEncoding.EncodedLen(len(sealed)))
	base64.RawURLEncoding.Encode(encoded, sealed)
	return encoded, nil
}

func (s *RedisAuthorizationCodeStore) open(value []byte, out any) error {
	decoded, err := base64.RawURLEncoding.DecodeString(string(value))
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(decoded) < gcm.NonceSize() {
		if err == nil {
			err = errors.New("invalid encrypted OAuth authorization code")
		}
		return err
	}
	plaintext, err := gcm.Open(nil, decoded[:gcm.NonceSize()], decoded[gcm.NonceSize():], nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(plaintext, out)
}
