package relay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// MCPSessionRegistry is the shared ownership registry for MCP stream
// sessions. It prevents an instance from adopting an unknown session without
// proving that the authenticated subject owns it.
type MCPSessionRegistry interface {
	Register(context.Context, string, string, time.Duration) error
	Owner(context.Context, string) (string, error)
	Revoke(context.Context, string) error
}

type RedisMCPSessionRegistry struct {
	client redis.UniversalClient
	prefix string
}

func NewRedisMCPSessionRegistry(client redis.UniversalClient, prefix string) (*RedisMCPSessionRegistry, error) {
	if client == nil {
		return nil, ErrRedisClientRequired
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "code-relay:"
	}
	return &RedisMCPSessionRegistry{client: client, prefix: prefix}, nil
}

func (s *RedisMCPSessionRegistry) key(id string) string {
	return s.prefix + "mcp-session:" + strings.TrimSpace(id)
}

func (s *RedisMCPSessionRegistry) Register(ctx context.Context, id, owner string, ttl time.Duration) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(owner) == "" || ttl <= 0 {
		return errors.New("MCP session id, owner, and positive TTL are required")
	}
	return s.client.Set(ctx, s.key(id), owner, ttl).Err()
}

func (s *RedisMCPSessionRegistry) Owner(ctx context.Context, id string) (string, error) {
	owner, err := s.client.Get(ctx, s.key(id)).Result()
	if err == redis.Nil {
		return "", ErrSessionNotFound
	}
	return owner, err
}

func (s *RedisMCPSessionRegistry) Revoke(ctx context.Context, id string) error {
	if err := s.client.Del(ctx, s.key(id)).Err(); err != nil {
		return err
	}
	return nil
}

// DistributedRateLimiter provides an atomic fixed-window limiter shared by
// all gateway instances.
type DistributedRateLimiter interface {
	Allow(context.Context, string, int, time.Duration) (bool, error)
}

type RedisRateLimiter struct {
	client redis.UniversalClient
	prefix string
}

func NewRedisRateLimiter(client redis.UniversalClient, prefix string) (*RedisRateLimiter, error) {
	if client == nil {
		return nil, ErrRedisClientRequired
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "code-relay:"
	}
	return &RedisRateLimiter{client: client, prefix: prefix}, nil
}

var rateLimitScript = redis.NewScript(`
local current = redis.call('INCR', KEYS[1])
if current == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]) end
return current
`)

func (s *RedisRateLimiter) Allow(ctx context.Context, subject string, limit int, window time.Duration) (bool, error) {
	if limit <= 0 || window <= 0 {
		return false, errors.New("rate limit and window must be positive")
	}
	bucket := time.Now().Unix() / int64(window/time.Second)
	key := fmt.Sprintf("%srate:%s:%d", s.prefix, strings.TrimSpace(subject), bucket)
	value, err := rateLimitScript.Run(ctx, s.client, []string{key}, int64(window/time.Second)).Int64()
	if err != nil {
		return false, err
	}
	return value <= int64(limit), nil
}

// DistributedLock is a short-lived lock with owner-token release semantics.
type DistributedLock interface {
	Acquire(context.Context, string, time.Duration) (func(context.Context) error, error)
}

type RedisDistributedLock struct {
	client redis.UniversalClient
	prefix string
}

func NewRedisDistributedLock(client redis.UniversalClient, prefix string) (*RedisDistributedLock, error) {
	if client == nil {
		return nil, ErrRedisClientRequired
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "code-relay:"
	}
	return &RedisDistributedLock{client: client, prefix: prefix}, nil
}

func (s *RedisDistributedLock) Acquire(ctx context.Context, name string, ttl time.Duration) (func(context.Context) error, error) {
	if strings.TrimSpace(name) == "" || ttl <= 0 {
		return nil, errors.New("lock name and positive TTL are required")
	}
	token, err := opaqueToken()
	if err != nil {
		return nil, err
	}
	key := s.prefix + "lock:" + strings.TrimSpace(name)
	ok, err := s.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("distributed lock is busy")
	}
	release := func(releaseCtx context.Context) error {
		return redisReleaseLock.Run(releaseCtx, s.client, []string{key}, token).Err()
	}
	return release, nil
}

var redisReleaseLock = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) else return 0 end
`)
