package relay

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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

var ErrEventCursorExpired = errors.New("event cursor is older than retained history")

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
	if afterID != "0-0" {
		info, err := s.client.XInfoStream(ctx, s.key(sessionID)).Result()
		if err != nil && err != redis.Nil {
			return nil, err
		}
		if err == nil && info.FirstEntry.ID != "" && redisStreamIDLess(afterID, info.FirstEntry.ID) {
			return nil, ErrEventCursorExpired
		}
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

func redisStreamIDLess(left, right string) bool {
	parse := func(value string) (uint64, uint64, bool) {
		parts := strings.SplitN(value, "-", 2)
		if len(parts) != 2 {
			return 0, 0, false
		}
		ms, err1 := strconv.ParseUint(parts[0], 10, 64)
		seq, err2 := strconv.ParseUint(parts[1], 10, 64)
		return ms, seq, err1 == nil && err2 == nil
	}
	leftMS, leftSeq, leftOK := parse(left)
	rightMS, rightSeq, rightOK := parse(right)
	if !leftOK || !rightOK {
		return left < right
	}
	return leftMS < rightMS || (leftMS == rightMS && leftSeq < rightSeq)
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
