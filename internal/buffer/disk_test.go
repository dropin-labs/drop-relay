package buffer

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestDiskBuffer_OrphanedTmpPurgedOnStartup(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "drop-relay-orphan-test-*")
	if err != nil {
		t.Fatalf("failed creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Pre-create tmp directory and simulate a half-written crashed .tmp file
	orphanTmp := filepath.Join(tmpDir, "tmp", "crashed_write.tmp")
	if err := os.MkdirAll(filepath.Dir(orphanTmp), 0750); err != nil {
		t.Fatalf("failed creating tmp dir: %v", err)
	}
	if err := os.WriteFile(orphanTmp, []byte("corrupt-partial-json"), 0600); err != nil {
		t.Fatalf("failed writing simulated crashed tmp file: %v", err)
	}

	// Initialize DiskBuffer — should automatically sweep orphaned .tmp files
	buf, err := NewDiskBuffer(tmpDir, 100)
	if err != nil {
		t.Fatalf("failed initializing disk buffer: %v", err)
	}
	defer buf.Close()

	if _, err := os.Stat(orphanTmp); !os.IsNotExist(err) {
		t.Errorf("expected orphaned .tmp file to be purged on startup, but it still exists")
	}
}

func TestDiskBuffer_OverflowRecoveryBeyondCapacity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "drop-relay-overflow-test-*")
	if err != nil {
		t.Fatalf("failed creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	spoolDir := filepath.Join(tmpDir, "spool")
	if err := os.MkdirAll(spoolDir, 0750); err != nil {
		t.Fatalf("failed creating spool dir: %v", err)
	}

	// Pre-seed 10 messages into spool on disk
	const totalMessages = 10
	for i := 0; i < totalMessages; i++ {
		id := fmt.Sprintf("overflow_msg_%02d", i)
		msg := &Message{
			ID:         id,
			From:       "sender@example.com",
			To:         []string{"recipient@example.com"},
			Subject:    "Overflow Test " + id,
			EnqueuedAt: time.Now().UTC(),
		}
		data, _ := json.Marshal(msg)
		if err := os.WriteFile(filepath.Join(spoolDir, id+".json"), data, 0600); err != nil {
			t.Fatalf("failed seeding spool file: %v", err)
		}
	}

	// Initialize buffer with capacity of only 3 (less than total 10)
	buf, err := NewDiskBuffer(tmpDir, 3)
	if err != nil {
		t.Fatalf("failed initializing disk buffer: %v", err)
	}
	defer buf.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Dequeue all 10 messages — the overflow drainer must stream them into queue
	dequeuedIDs := make(map[string]bool)
	for i := 0; i < totalMessages; i++ {
		msg, err := buf.Dequeue(ctx)
		if err != nil {
			t.Fatalf("failed dequeuing message %d: %v", i, err)
		}
		dequeuedIDs[msg.ID] = true
		_ = buf.Ack(ctx, msg.ID)
	}

	if len(dequeuedIDs) != totalMessages {
		t.Fatalf("expected %d unique messages dequeued, got %d", totalMessages, len(dequeuedIDs))
	}

	depth, _ := buf.Depth(ctx)
	if depth != 0 {
		t.Fatalf("expected depth 0 after acking all, got %d", depth)
	}
}
