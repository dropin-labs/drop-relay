package buffer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiskBuffer_EnqueueDequeueAck(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "drop-relay-disk-test-*")
	if err != nil {
		t.Fatalf("failed creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	buf, err := NewDiskBuffer(tmpDir, 100)
	if err != nil {
		t.Fatalf("failed initializing disk buffer: %v", err)
	}
	defer buf.Close()

	ctx := context.Background()
	msg := &Message{
		ID:         "test_msg_001",
		From:       "sender@example.com",
		To:         []string{"recipient@example.com"},
		Subject:    "Persistent Disk Test",
		Text:       "Hello from disk buffer",
		EnqueuedAt: time.Now().UTC(),
	}

	if err := buf.Enqueue(ctx, msg); err != nil {
		t.Fatalf("failed enqueuing: %v", err)
	}

	// Verify file exists on disk
	spoolFile := filepath.Join(tmpDir, "spool", "test_msg_001.json")
	if _, err := os.Stat(spoolFile); err != nil {
		t.Fatalf("expected spool file to exist on disk: %v", err)
	}

	// Dequeue
	deqMsg, err := buf.Dequeue(ctx)
	if err != nil {
		t.Fatalf("failed dequeuing: %v", err)
	}
	if deqMsg.ID != "test_msg_001" {
		t.Errorf("expected msg test_msg_001, got: %s", deqMsg.ID)
	}

	// Ack
	if err := buf.Ack(ctx, deqMsg.ID); err != nil {
		t.Fatalf("failed acking: %v", err)
	}

	// Verify file is unlinked on disk
	if _, err := os.Stat(spoolFile); !os.IsNotExist(err) {
		t.Errorf("expected spool file to be unlinked after Ack")
	}
}

func TestDiskBuffer_CrashRecoveryAcrossRestarts(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "drop-relay-recovery-test-*")
	if err != nil {
		t.Fatalf("failed creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctx := context.Background()

	// 1. First instance writes 3 messages then crashes/stops
	buf1, err := NewDiskBuffer(tmpDir, 100)
	if err != nil {
		t.Fatalf("failed initializing disk buffer: %v", err)
	}

	for _, id := range []string{"msg_a", "msg_b", "msg_c"} {
		msg := &Message{
			ID:         id,
			From:       "sender@example.com",
			To:         []string{"recipient@example.com"},
			Subject:    "Recovery Test " + id,
			EnqueuedAt: time.Now().UTC(),
		}
		if err := buf1.Enqueue(ctx, msg); err != nil {
			t.Fatalf("failed enqueuing: %v", err)
		}
	}

	// Simulate sudden termination
	_ = buf1.Close()

	// 2. Second instance starts up pointing to same directory
	buf2, err := NewDiskBuffer(tmpDir, 100)
	if err != nil {
		t.Fatalf("failed initializing recovered disk buffer: %v", err)
	}
	defer buf2.Close()

	depth, err := buf2.Depth(ctx)
	if err != nil {
		t.Fatalf("failed getting depth: %v", err)
	}
	if depth != 3 {
		t.Errorf("expected recovered depth of 3, got: %d", depth)
	}

	// Verify all 3 can be dequeued cleanly
	recoveredIDs := make(map[string]bool)
	for i := 0; i < 3; i++ {
		deq, err := buf2.Dequeue(ctx)
		if err != nil {
			t.Fatalf("failed dequeuing recovered item %d: %v", i, err)
		}
		recoveredIDs[deq.ID] = true
		_ = buf2.Ack(ctx, deq.ID)
	}

	for _, id := range []string{"msg_a", "msg_b", "msg_c"} {
		if !recoveredIDs[id] {
			t.Errorf("expected recovered message %s was not retrieved", id)
		}
	}
}

func TestDiskBuffer_ScheduledDispatch(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "drop-relay-sched-test-*")
	if err != nil {
		t.Fatalf("failed creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	buf, err := NewDiskBuffer(tmpDir, 100)
	if err != nil {
		t.Fatalf("failed initializing disk buffer: %v", err)
	}
	defer buf.Close()

	ctx := context.Background()
	scheduledTime := time.Now().UTC().Add(150 * time.Millisecond)
	msg := &Message{
		ID:         "sched_001",
		From:       "sender@example.com",
		To:         []string{"recipient@example.com"},
		Subject:    "Scheduled Email",
		SendAt:     scheduledTime,
		EnqueuedAt: time.Now().UTC(),
	}

	if err := buf.Enqueue(ctx, msg); err != nil {
		t.Fatalf("failed enqueuing scheduled message: %v", err)
	}

	// Immediate dequeue with short timeout should timeout because message is scheduled for future
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	_, err = buf.Dequeue(timeoutCtx)
	cancel()
	if err == nil {
		t.Fatalf("expected Dequeue to wait for SendAt, but got message immediately")
	}

	// Dequeue with context covering the scheduled time should succeed
	waitCtx, waitCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer waitCancel()
	deqMsg, err := buf.Dequeue(waitCtx)
	if err != nil {
		t.Fatalf("expected Dequeue to succeed after SendAt: %v", err)
	}
	if deqMsg.ID != "sched_001" {
		t.Fatalf("expected msg sched_001, got: %s", deqMsg.ID)
	}
	_ = buf.Ack(ctx, deqMsg.ID)
}
