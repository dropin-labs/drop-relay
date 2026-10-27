package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dropin-labs/drop-relay/internal/buffer"
	"github.com/dropin-labs/drop-relay/internal/config"
)

func setupTestServer() (*Server, buffer.Buffer) {
	cfg := &config.Config{
		Domain:         "example.com",
		Hostname:       "mail.example.com",
		PortHTTP:       8080,
		APIKey:         "secret-test-token",
		BufferType:     "memory",
		BufferCapacity: 100,
		MaxMessageSize: 1048576,
	}
	buf := buffer.NewMemoryBuffer(100)
	srv := NewServer(cfg, buf)
	return srv, buf
}

func TestServer_AuthUnauthorized(t *testing.T) {
	srv, buf := setupTestServer()
	defer buf.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/send", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 Unauthorized, got %d", rec.Code)
	}
}

func TestServer_SendSuccess(t *testing.T) {
	srv, buf := setupTestServer()
	defer buf.Close()

	payload := map[string]interface{}{
		"from":     "alerts@example.com",
		"to":       "user@example.com",
		"subject":  "Test Alert",
		"text":     "Everything is fine",
		"html":     "<p>Everything is fine</p>",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/v1/send", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer secret-test-token")
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("Expected 202 Accepted, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var resp SendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp.Status != "queued" || resp.ID == "" {
		t.Fatalf("Invalid response structure: %+v", resp)
	}

	depth, _ := buf.Depth(context.Background())
	if depth != 1 {
		t.Fatalf("Expected buffer depth 1, got %d", depth)
	}
}

func TestServer_HealthAndReady(t *testing.T) {
	srv, buf := setupTestServer()
	defer buf.Close()

	// Healthz
	reqHealth := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recHealth := httptest.NewRecorder()
	srv.mux.ServeHTTP(recHealth, reqHealth)
	if recHealth.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for /healthz, got %d", recHealth.Code)
	}

	// Readyz
	reqReady := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	recReady := httptest.NewRecorder()
	srv.mux.ServeHTTP(recReady, reqReady)
	if recReady.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for /readyz, got %d", recReady.Code)
	}

	// Metrics
	reqMetrics := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	recMetrics := httptest.NewRecorder()
	srv.mux.ServeHTTP(recMetrics, reqMetrics)
	if recMetrics.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for /metrics, got %d", recMetrics.Code)
	}
}

func TestServer_SendScheduled_Delay(t *testing.T) {
	srv, buf := setupTestServer()
	defer buf.Close()

	payload := map[string]interface{}{
		"from":    "alerts@example.com",
		"to":      "user@example.com",
		"subject": "Delayed Email",
		"text":    "See you in 10s",
		"delay":   "10s",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/v1/send", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer secret-test-token")
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("Expected 202 Accepted, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var resp SendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp.Status != "scheduled" || resp.SendAt == nil {
		t.Fatalf("Expected status 'scheduled' with send_at, got: %+v", resp)
	}
}

func TestServer_SendScheduled_Validation(t *testing.T) {
	srv, buf := setupTestServer()
	defer buf.Close()

	// Both send_at and delay should fail with 422
	payloadBoth := map[string]interface{}{
		"from":    "alerts@example.com",
		"to":      "user@example.com",
		"subject": "Both",
		"delay":   "10s",
		"send_at": "2026-10-27T17:00:00Z",
	}
	body, _ := json.Marshal(payloadBoth)
	req := httptest.NewRequest(http.MethodPost, "/v1/send", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer secret-test-token")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Expected 422 Unprocessable Entity for both delay and send_at, got %d", rec.Code)
	}

	// Invalid delay duration
	payloadBadDelay := map[string]interface{}{
		"from":    "alerts@example.com",
		"to":      "user@example.com",
		"subject": "Bad Delay",
		"delay":   "not-a-duration",
	}
	body, _ = json.Marshal(payloadBadDelay)
	req = httptest.NewRequest(http.MethodPost, "/v1/send", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer secret-test-token")
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Expected 422 for invalid delay, got %d", rec.Code)
	}
}
