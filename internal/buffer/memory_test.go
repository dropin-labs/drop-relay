package buffer

import (
	"context"
	"testing"
	"time"
)

func TestMemoryBuffer_EnqueueDequeue(t *testing.T) {
	buf := NewMemoryBuffer(5)
	defer buf.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	msg := &Message{
		ID:      "msg-1",
		From:    "sender@example.com",
		To:      []string{"recipient@example.com"},
		Subject: "Test Subject",
		Text:    "Test Body",
	}

	if err := buf.Enqueue(ctx, msg); err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	depth, err := buf.Depth(ctx)
	if err != nil || depth != 1 {
		t.Fatalf("Expected depth 1, got %d (err: %v)", depth, err)
	}

	deq, err := buf.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue failed: %v", err)
	}

	if deq.ID != "msg-1" {
		t.Fatalf("Expected msg-1, got %s", deq.ID)
	}

	if err := buf.Ack(ctx, deq.ID); err != nil {
		t.Fatalf("Ack failed: %v", err)
	}
}

func TestMemoryBuffer_CapacityExceeded(t *testing.T) {
	buf := NewMemoryBuffer(2)
	defer buf.Close()

	ctx := context.Background()

	_ = buf.Enqueue(ctx, &Message{ID: "msg-1"})
	_ = buf.Enqueue(ctx, &Message{ID: "msg-2"})

	err := buf.Enqueue(ctx, &Message{ID: "msg-3"})
	if err != ErrBufferFull {
		t.Fatalf("Expected ErrBufferFull, got %v", err)
	}
}
