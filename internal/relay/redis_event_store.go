package relay

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisSessionEventStore uses Redis Streams so every gateway instance observes
// the same ordered event history and reconnect cursors.
type RedisSessionEventStore struct {
	client redis.UniversalClient
	prefix string
}

func NewRedisSessionEventStore(client redis.UniversalClient, prefix string) (*RedisSessionEventStore, error) {
	if client == nil {
		return nil, ErrRedisClientRequired
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "code-relay:"
	}
	return &RedisSessionEventStore{client: client, prefix: prefix}, nil
}

func (s *RedisSessionEventStore) key(sessionID string) string {
	return s.prefix + "session-events:" + sessionID
}

func (s *RedisSessionEventStore) Publish(ctx context.Context, sessionID string, data []byte, maxLen int) (string, error) {
	args := &redis.XAddArgs{Stream: s.key(sessionID), Values: map[string]any{"data": data}}
	if maxLen > 0 {
		args.MaxLen = int64(maxLen)
		args.Approx = true
	}
	return s.client.XAdd(ctx, args).Result()
}

func (s *RedisSessionEventStore) Read(ctx context.Context, sessionID, afterID string, block time.Duration) ([]SessionEvent, error) {
	if strings.TrimSpace(afterID) == "" {
		afterID = "0-0"
	}
	result, err := s.client.XRead(ctx, &redis.XReadArgs{Streams: []string{s.key(sessionID), afterID}, Count: 64, Block: block}).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	events := make([]SessionEvent, 0)
	for _, stream := range result {
		for _, message := range stream.Messages {
			value, ok := message.Values["data"]
			if !ok {
				continue
			}
			var data []byte
			switch typed := value.(type) {
			case string:
				data = []byte(typed)
			case []byte:
				data = typed
			default:
				data = []byte(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(toString(typed)), "["), "]")))
			}
			events = append(events, SessionEvent{ID: message.ID, Data: data})
		}
	}
	return events, nil
}

func toString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}
