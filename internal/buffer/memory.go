package buffer

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// MemoryBuffer is an in-memory channel-backed buffer with bounded capacity and in-flight tracking.
type MemoryBuffer struct {
	queue    chan *Message
	inFlight map[string]*Message
	mu       sync.RWMutex
	capacity int
	closed   atomic.Bool
	stopCh   chan struct{}
	sendWg   sync.WaitGroup
	timersMu sync.Mutex
	timers   map[string]*time.Timer
}

// NewMemoryBuffer creates a thread-safe bounded in-memory buffer.
func NewMemoryBuffer(capacity int) *MemoryBuffer {
	if capacity <= 0 {
		capacity = 10000
	}
	return &MemoryBuffer{
		queue:    make(chan *Message, capacity),
		inFlight: make(map[string]*Message),
		capacity: capacity,
		stopCh:   make(chan struct{}),
		timers:   make(map[string]*time.Timer),
	}
}

// Enqueue adds a message to the buffer or schedules it for deferred dispatch.
func (b *MemoryBuffer) Enqueue(ctx context.Context, msg *Message) error {
	if b.closed.Load() {
		return ErrBufferClosed
	}

	if msg == nil {
		return fmt.Errorf("cannot enqueue nil message")
	}

	if msg.EnqueuedAt.IsZero() {
		msg.EnqueuedAt = time.Now().UTC()
	}

	if !msg.SendAt.IsZero() && msg.SendAt.After(time.Now().UTC()) {
		b.schedule(msg)
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.stopCh:
		return ErrBufferClosed
	case b.queue <- msg:
		return nil
	default:
		return ErrBufferFull
	}
}

func (b *MemoryBuffer) schedule(msg *Message) {
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

func (b *MemoryBuffer) sendQueue(msg *Message) bool {
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

// Dequeue retrieves the next message from the buffer, blocking until one is available or context ends.
func (b *MemoryBuffer) Dequeue(ctx context.Context) (*Message, error) {
	if b.closed.Load() && len(b.queue) == 0 {
		return nil, ErrBufferClosed
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.stopCh:
		// Attempt non-blocking drain if items remain
		select {
		case msg, ok := <-b.queue:
			if ok && msg != nil {
				b.trackInFlight(msg)
				return msg, nil
			}
		default:
		}
		return nil, ErrBufferClosed
	case msg, ok := <-b.queue:
		if !ok || msg == nil {
			return nil, ErrBufferClosed
		}
		b.trackInFlight(msg)
		return msg, nil
	}
}

func (b *MemoryBuffer) trackInFlight(msg *Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.inFlight[msg.ID] = msg
}

// Ack confirms successful processing of a message and removes it from the in-flight tracker.
func (b *MemoryBuffer) Ack(_ context.Context, id string) error {
	b.timersMu.Lock()
	if t, ok := b.timers[id]; ok {
		t.Stop()
		delete(b.timers, id)
	}
	b.timersMu.Unlock()

	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.inFlight, id)
	return nil
}

// Nack handles failed processing by incrementing retry counter and scheduling re-enqueue with backoff.
func (b *MemoryBuffer) Nack(ctx context.Context, id string, err error) error {
	b.mu.Lock()
	msg, exists := b.inFlight[id]
	if exists {
		delete(b.inFlight, id)
	}
	b.mu.Unlock()

	if !exists || msg == nil {
		return ErrNotFound
	}

	msg.Attempts++
	if err != nil {
		msg.LastError = err.Error()
	}

	const maxAttempts = 5
	if msg.Attempts >= maxAttempts {
		// Exceeded max retry attempts; discard from active buffer
		return fmt.Errorf("message %s dropped: exceeded max retry attempts (%d)", id, maxAttempts)
	}

	// Schedule exponential backoff in background goroutine
	backoff := time.Duration(1<<uint(msg.Attempts-1)) * time.Second
	go func() {
		select {
		case <-time.After(backoff):
			_ = b.Enqueue(context.Background(), msg)
		case <-b.stopCh:
		}
	}()

	return nil
}

// Depth returns the current number of queued messages waiting for dispatch.
func (b *MemoryBuffer) Depth(_ context.Context) (int64, error) {
	return int64(len(b.queue)), nil
}

// Close shuts down the buffer cleanly.
func (b *MemoryBuffer) Close() error {
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
	return nil
}
