package buffer

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrBufferFull indicates that the buffer has reached maximum capacity.
	ErrBufferFull = errors.New("buffer capacity exceeded")
	// ErrBufferClosed indicates that the buffer has been closed.
	ErrBufferClosed = errors.New("buffer is closed")
	// ErrNotFound indicates the specified message was not found.
	ErrNotFound = errors.New("message not found in buffer")
	// ErrTimeout indicates a dequeue operation timed out.
	ErrTimeout = errors.New("dequeue operation timed out")
)

// Message encapsulates a normalized email ready for transport.
type Message struct {
	ID         string            `json:"id"`
	From       string            `json:"from"`
	FromName   string            `json:"from_name,omitempty"`
	To         []string          `json:"to"`
	Cc         []string          `json:"cc,omitempty"`
	Bcc        []string          `json:"bcc,omitempty"`
	Subject    string            `json:"subject"`
	Text       string            `json:"text,omitempty"`
	HTML       string            `json:"html,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Raw        []byte            `json:"raw,omitempty"`
	Attempts   int               `json:"attempts"`
	LastError  string            `json:"last_error,omitempty"`
	SendAt     time.Time         `json:"send_at,omitempty"`
	EnqueuedAt time.Time         `json:"enqueued_at"`
}

// Buffer defines the contract for email spooling and backpressure control.
type Buffer interface {
	Enqueue(ctx context.Context, msg *Message) error
	Dequeue(ctx context.Context) (*Message, error)
	Ack(ctx context.Context, id string) error
	Nack(ctx context.Context, id string, err error) error
	Depth(ctx context.Context) (int64, error)
	Close() error
}
