package http

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/dropin-labs/drop-relay/internal/buffer"
	"github.com/dropin-labs/drop-relay/internal/config"
	"github.com/dropin-labs/drop-relay/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Server implements the HTTP ingestion and observability API.
type Server struct {
	cfg    *config.Config
	buffer buffer.Buffer
	mux    *http.ServeMux
	server *http.Server
}

// SendRequest represents the payload for POST /v1/send.
type SendRequest struct {
	From     string            `json:"from"`
	FromName string            `json:"from_name,omitempty"`
	To       interface{}       `json:"to"` // accepts string or []string
	Cc       []string          `json:"cc,omitempty"`
	Bcc      []string          `json:"bcc,omitempty"`
	Subject  string            `json:"subject"`
	Text     string            `json:"text,omitempty"`
	HTML     string            `json:"html,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

// SendResponse represents the response returned after enqueuing.
type SendResponse struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

// ErrorResponse defines standard JSON error responses.
type ErrorResponse struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
}

// NewServer initializes the HTTP server with configured routes and security middleware.
func NewServer(cfg *config.Config, buf buffer.Buffer) *Server {
	mux := http.NewServeMux()
	s := &Server{
		cfg:    cfg,
		buffer: buf,
		mux:    mux,
	}

	mux.HandleFunc("POST /v1/send", s.authMiddleware(s.handleSend))
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.Handle("GET /metrics", promhttp.Handler())

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.PortHTTP),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s
}

// ListenAndServe runs the HTTP server.
func (s *Server) ListenAndServe() error {
	return s.server.ListenAndServe()
}

// Close gracefully closes the HTTP server.
func (s *Server) Close() error {
	return s.server.Close()
}

// authMiddleware enforces Bearer token authentication via RELAY_API_KEY.
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.APIKey != "" {
			authHeader := r.Header.Get("Authorization")
			expected := "Bearer " + s.cfg.APIKey
			if subtle.ConstantTimeCompare([]byte(authHeader), []byte(expected)) != 1 {
				s.jsonError(w, http.StatusUnauthorized, "Unauthorized: Invalid or missing API key")
				metrics.IngestedTotal.WithLabelValues("http", "unauthorized").Inc()
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	if s.cfg.MaxMessageSize > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxMessageSize)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "Payload size exceeds maximum allowed limit or read failed")
		metrics.IngestedTotal.WithLabelValues("http", "rejected_payload_size").Inc()
		return
	}

	var req SendRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.jsonError(w, http.StatusBadRequest, "Invalid JSON payload: "+err.Error())
		metrics.IngestedTotal.WithLabelValues("http", "malformed_json").Inc()
		return
	}

	// Validate From
	fromAddr := strings.TrimSpace(req.From)
	if fromAddr == "" {
		s.jsonError(w, http.StatusUnprocessableEntity, "Field 'from' is required")
		metrics.IngestedTotal.WithLabelValues("http", "invalid_from").Inc()
		return
	}
	if _, err := mail.ParseAddress(fromAddr); err != nil {
		s.jsonError(w, http.StatusUnprocessableEntity, "Invalid email address in 'from': "+err.Error())
		metrics.IngestedTotal.WithLabelValues("http", "invalid_from").Inc()
		return
	}

	// Normalize & Validate Recipients
	recipients, err := extractRecipients(req.To)
	if err != nil || len(recipients) == 0 {
		s.jsonError(w, http.StatusUnprocessableEntity, "Field 'to' must contain at least one valid email address")
		metrics.IngestedTotal.WithLabelValues("http", "invalid_to").Inc()
		return
	}

	for _, to := range recipients {
		if _, err := mail.ParseAddress(to); err != nil {
			s.jsonError(w, http.StatusUnprocessableEntity, fmt.Sprintf("Invalid recipient email '%s': %v", to, err))
			metrics.IngestedTotal.WithLabelValues("http", "invalid_to").Inc()
			return
		}
	}

	// Normalize HTML Document Structure
	htmlBody := strings.TrimSpace(req.HTML)
	if htmlBody != "" && !strings.HasPrefix(strings.ToLower(htmlBody), "<!doctype html") && !strings.HasPrefix(strings.ToLower(htmlBody), "<html") {
		htmlBody = fmt.Sprintf("<!DOCTYPE html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"></head><body style=\"margin:0;padding:0;\">%s</body></html>", htmlBody)
	}

	// Normalize Text CRLF line endings
	textBody := strings.ReplaceAll(req.Text, "\r\n", "\n")
	textBody = strings.ReplaceAll(textBody, "\n", "\r\n")

	// Message ID generation
	msgID := fmt.Sprintf("msg_%d_%s", time.Now().UnixNano(), s.cfg.Domain)

	msg := &buffer.Message{
		ID:         msgID,
		From:       fromAddr,
		FromName:   strings.TrimSpace(req.FromName),
		To:         recipients,
		Cc:         req.Cc,
		Bcc:        req.Bcc,
		Subject:    req.Subject,
		Text:       textBody,
		HTML:       htmlBody,
		Headers:    req.Headers,
		EnqueuedAt: time.Now().UTC(),
	}

	if err := s.buffer.Enqueue(r.Context(), msg); err != nil {
		if err == buffer.ErrBufferFull {
			s.jsonError(w, http.StatusServiceUnavailable, "Relay spool buffer full; backpressure active")
			metrics.IngestedTotal.WithLabelValues("http", "buffer_full").Inc()
			return
		}
		s.jsonError(w, http.StatusInternalServerError, "Failed to spool message: "+err.Error())
		metrics.IngestedTotal.WithLabelValues("http", "enqueue_error").Inc()
		return
	}

	metrics.IngestedTotal.WithLabelValues("http", "accepted").Inc()
	metrics.QueueDepth.Inc()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(SendResponse{
		ID:        msgID,
		Status:    "queued",
		Timestamp: msg.EnqueuedAt,
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	depth, err := s.buffer.Depth(r.Context())
	if err != nil {
		s.jsonError(w, http.StatusServiceUnavailable, "Spool buffer unavailable: "+err.Error())
		return
	}

	metrics.QueueDepth.Set(float64(depth))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "ok",
		"buffer_depth": depth,
		"buffer_type":  s.cfg.BufferType,
	})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ready",
	})
}

func (s *Server) jsonError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(ErrorResponse{Error: message})
}

func extractRecipients(raw interface{}) ([]string, error) {
	switch v := raw.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil, fmt.Errorf("empty recipient")
		}
		return []string{trimmed}, nil
	case []interface{}:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out, nil
	case []string:
		var out []string
		for _, item := range v {
			if strings.TrimSpace(item) != "" {
				out = append(out, strings.TrimSpace(item))
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("invalid recipient type")
	}
}
