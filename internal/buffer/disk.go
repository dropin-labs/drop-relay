package buffer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// DiskBuffer implements a crash-durable, atomic disk spool buffer.
type DiskBuffer struct {
	baseDir  string
	spoolDir string
	tmpDir   string
	capacity int
	closed   atomic.Bool
	depth    atomic.Int64

	queue    chan *Message
	stopCh   chan struct{}
	mu       sync.RWMutex
	sendWg   sync.WaitGroup
	timersMu sync.Mutex
	timers   map[string]*time.Timer
}

// NewDiskBuffer initializes and mounts the persistent disk spool directory.
func NewDiskBuffer(baseDir string, capacity int) (*DiskBuffer, error) {
	if capacity <= 0 {
		capacity = 10000
	}
	if baseDir == "" {
		baseDir = "/var/spool/drop-relay"
	}

	spoolDir := filepath.Join(baseDir, "spool")
	tmpDir := filepath.Join(baseDir, "tmp")

	if err := os.MkdirAll(spoolDir, 0750); err != nil {
		return nil, fmt.Errorf("failed creating disk spool directory: %w", err)
	}
	if err := os.MkdirAll(tmpDir, 0750); err != nil {
		return nil, fmt.Errorf("failed creating disk tmp directory: %w", err)
	}

	b := &DiskBuffer{
		baseDir:  baseDir,
		spoolDir: spoolDir,
		tmpDir:   tmpDir,
		capacity: capacity,
		queue:    make(chan *Message, capacity),
		stopCh:   make(chan struct{}),
		timers:   make(map[string]*time.Timer),
	}

	// Crash Recovery: Hydrate existing spool entries on startup
	if err := b.recoverPending(); err != nil {
		return nil, fmt.Errorf("failed recovering disk spool on startup: %w", err)
	}

	return b, nil
}

// recoverPending scans the spool directory for un-dispatched messages and populates the in-memory queue or scheduler.
func (b *DiskBuffer) recoverPending() error {
	entries, err := os.ReadDir(b.spoolDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		filePath := filepath.Join(b.spoolDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue // Skip corrupted or unreadable entries
		}

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		b.depth.Add(1)

		if !msg.SendAt.IsZero() && msg.SendAt.After(time.Now().UTC()) {
			b.scheduleMessage(&msg)
		} else {
			select {
			case b.queue <- &msg:
			default:
				// Queue capacity reached during recovery
				break
			}
		}
	}

	return nil
}

// scheduleMessage arms a background timer to dispatch the message when its SendAt timestamp arrives.
func (b *DiskBuffer) scheduleMessage(msg *Message) {
	delay := time.Until(msg.SendAt)
	if delay <= 0 {
		b.sendQueue(msg)
		return
	}

	b.timersMu.Lock()
	defer b.timersMu.Unlock()

	if b.closed.Load() {
		return
	}

	timer := time.AfterFunc(delay, func() {
		b.timersMu.Lock()
		delete(b.timers, msg.ID)
		b.timersMu.Unlock()

		b.sendQueue(msg)
	})

	b.timers[msg.ID] = timer
}

// sendQueue safely attempts to enqueue a message into the dispatch queue without risking panic on closed channel.
func (b *DiskBuffer) sendQueue(msg *Message) bool {
	b.mu.RLock()
	if b.closed.Load() {
		b.mu.RUnlock()
		return false
	}
	b.sendWg.Add(1)
	b.mu.RUnlock()
	defer b.sendWg.Done()

	select {
	case <-b.stopCh:
		return false
	case b.queue <- msg:
		return true
	}
}

// Enqueue atomically persists a message to disk and queues it for dispatch or schedules it.
func (b *DiskBuffer) Enqueue(ctx context.Context, msg *Message) error {
	if b.closed.Load() {
		return ErrBufferClosed
	}

	if int(b.depth.Load()) >= b.capacity {
		return ErrBufferFull
	}

	if msg.EnqueuedAt.IsZero() {
		msg.EnqueuedAt = time.Now().UTC()
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to serialize message: %w", err)
	}

	// 1. Atomic write to tmp file with fsync
	tmpFile := filepath.Join(b.tmpDir, fmt.Sprintf("%s.tmp", msg.ID))
	f, err := os.OpenFile(tmpFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed opening disk tmp file: %w", err)
	}

	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed writing message to disk: %w", err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed syncing message to disk block storage: %w", err)
	}
	_ = f.Close()

	// 2. Atomic POSIX rename into active spool
	spoolFile := filepath.Join(b.spoolDir, fmt.Sprintf("%s.json", msg.ID))
	if err := os.Rename(tmpFile, spoolFile); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed renaming message to spool file: %w", err)
	}

	b.depth.Add(1)

	// 3. If scheduled in the future, register with timer
	if !msg.SendAt.IsZero() && msg.SendAt.After(time.Now().UTC()) {
		b.scheduleMessage(msg)
		return nil
	}

	// 4. Push to ready dispatch queue
	b.mu.RLock()
	if b.closed.Load() {
		b.mu.RUnlock()
		_ = os.Remove(spoolFile)
		b.depth.Add(-1)
		return ErrBufferClosed
	}
	b.sendWg.Add(1)
	b.mu.RUnlock()
	defer b.sendWg.Done()

	select {
	case <-ctx.Done():
		_ = os.Remove(spoolFile)
		b.depth.Add(-1)
		return ctx.Err()
	case <-b.stopCh:
		_ = os.Remove(spoolFile)
		b.depth.Add(-1)
		return ErrBufferClosed
	case b.queue <- msg:
		return nil
	}
}

// Dequeue retrieves the next message ready for dispatch.
func (b *DiskBuffer) Dequeue(ctx context.Context) (*Message, error) {
	if b.closed.Load() && len(b.queue) == 0 {
		return nil, ErrBufferClosed
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.stopCh:
		return nil, ErrBufferClosed
	case msg, ok := <-b.queue:
		if !ok {
			return nil, ErrBufferClosed
		}
		return msg, nil
	}
}

// Ack confirms delivery and unlinks the spool file from disk.
func (b *DiskBuffer) Ack(_ context.Context, id string) error {
	b.timersMu.Lock()
	if t, ok := b.timers[id]; ok {
		t.Stop()
		delete(b.timers, id)
	}
	b.timersMu.Unlock()

	spoolFile := filepath.Join(b.spoolDir, fmt.Sprintf("%s.json", id))
	if err := os.Remove(spoolFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed unlinking spool file: %w", err)
	}
	b.depth.Add(-1)
	return nil
}

// Nack handles transient delivery failure, updating attempts and re-enqueueing.
func (b *DiskBuffer) Nack(ctx context.Context, id string, lastErr error) error {
	spoolFile := filepath.Join(b.spoolDir, fmt.Sprintf("%s.json", id))
	data, err := os.ReadFile(spoolFile)
	if err != nil {
		return nil // File may have been removed
	}

	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return err
	}

	msg.Attempts++
	if lastErr != nil {
		msg.LastError = lastErr.Error()
	}

	// Re-serialize with updated attempt count
	if payload, err := json.Marshal(&msg); err == nil {
		_ = os.WriteFile(spoolFile, payload, 0600)
	}

	// Exponential backoff delay before re-enqueue
	delay := time.Duration(1<<msg.Attempts) * 100 * time.Millisecond
	if delay > 5*time.Second {
		delay = 5 * time.Second
	}

	go func() {
		time.Sleep(delay)
		select {
		case <-ctx.Done():
		case <-b.stopCh:
		default:
			b.sendQueue(&msg)
		}
	}()

	return nil
}

// Depth returns the current active count of messages in the spool buffer.
func (b *DiskBuffer) Depth(_ context.Context) (int64, error) {
	d := b.depth.Load()
	if d < 0 {
		return 0, nil
	}
	return d, nil
}

// Close closes the queue and stops accepting new items, cleaning up active timers.
func (b *DiskBuffer) Close() error {
	b.mu.Lock()
	if !b.closed.CompareAndSwap(false, true) {
		b.mu.Unlock()
		return nil
	}
	close(b.stopCh)

	b.timersMu.Lock()
	for _, t := range b.timers {
		t.Stop()
	}
	b.timers = make(map[string]*time.Timer)
	b.timersMu.Unlock()

	b.mu.Unlock()

	b.sendWg.Wait()
	close(b.queue)
	return nil
}
