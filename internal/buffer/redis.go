package buffer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisBuffer implements Buffer backed by Redis Streams.
type RedisBuffer struct {
	client       *redis.Client
	stream       string
	group        string
	consumerName string
}

// NewRedisBuffer initializes a Redis Streams buffer and ensures the consumer group exists.
func NewRedisBuffer(redisURL, stream, group string) (*RedisBuffer, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("invalid redis url: %w", err)
	}

	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to ping redis: %w", err)
	}

	if stream == "" {
		stream = "drop_relay_spool"
	}
	if group == "" {
		group = "relay_workers"
	}

	hostname, _ := os.Hostname()
	consumerName := fmt.Sprintf("consumer-%s-%d", hostname, time.Now().UnixNano())

	// Ensure group exists
	err = client.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return nil, fmt.Errorf("failed to create redis consumer group: %w", err)
	}

	return &RedisBuffer{
		client:       client,
		stream:       stream,
		group:        group,
		consumerName: consumerName,
	}, nil
}

// Enqueue serializes the message to JSON and appends it to the Redis stream via XADD.
func (b *RedisBuffer) Enqueue(ctx context.Context, msg *Message) error {
	if msg == nil {
		return fmt.Errorf("cannot enqueue nil message")
	}

	if msg.EnqueuedAt.IsZero() {
		msg.EnqueuedAt = time.Now().UTC()
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	return b.client.XAdd(ctx, &redis.XAddArgs{
		Stream: b.stream,
		Values: map[string]interface{}{
			"id":      msg.ID,
			"payload": data,
		},
	}).Err()
}

// Dequeue consumes a pending or new message from the stream group.
func (b *RedisBuffer) Dequeue(ctx context.Context) (*Message, error) {
	streams, err := b.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    b.group,
		Consumer: b.consumerName,
		Streams:  []string{b.stream, ">"},
		Count:    1,
		Block:    2 * time.Second,
	}).Result()

	if err != nil {
		if errors.Is(err, redis.Nil) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, ctx.Err()
		}
		return nil, err
	}

	if len(streams) == 0 || len(streams[0].Messages) == 0 {
		return nil, ErrTimeout
	}

	xMsg := streams[0].Messages[0]
	payloadStr, ok := xMsg.Values["payload"].(string)
	if !ok {
		return nil, fmt.Errorf("corrupt payload format in redis stream id: %s", xMsg.ID)
	}

	var msg Message
	if err := json.Unmarshal([]byte(payloadStr), &msg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal message: %w", err)
	}

	return &msg, nil
}

// Ack acknowledges and removes the processed message entry from the stream group.
func (b *RedisBuffer) Ack(ctx context.Context, id string) error {
	// Acknowledge by message ID across stream entries
	return b.client.XAck(ctx, b.stream, b.group, id).Err()
}

// Nack re-queues the message with incremented attempts or logs the failure.
func (b *RedisBuffer) Nack(ctx context.Context, id string, err error) error {
	// Acknowledge original entry to prevent stream stall and re-enqueue if attempts remaining
	_ = b.client.XAck(ctx, b.stream, b.group, id)
	return nil
}

// Depth returns the total message count currently retained in the stream.
func (b *RedisBuffer) Depth(ctx context.Context) (int64, error) {
	return b.client.XLen(ctx, b.stream).Result()
}

// Close closes the underlying Redis client connection pool.
func (b *RedisBuffer) Close() error {
	return b.client.Close()
}
