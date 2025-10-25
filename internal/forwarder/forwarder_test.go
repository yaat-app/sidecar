package forwarder

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yaat/sidecar/internal/buffer"
)

func TestNew(t *testing.T) {
	endpoint := "https://api.test.com/ingest"
	apiKey := "test-api-key"

	f := New(endpoint, apiKey)

	if f == nil {
		t.Fatal("New() returned nil")
	}

	if f.apiEndpoint != endpoint {
		t.Errorf("Expected endpoint %s, got %s", endpoint, f.apiEndpoint)
	}

	if f.apiKey != apiKey {
		t.Errorf("Expected API key %s, got %s", apiKey, f.apiKey)
	}

	if f.client == nil {
		t.Error("HTTP client is nil")
	}
}

func TestSendEmpty(t *testing.T) {
	f := New("https://test.com", "key")

	err := f.Send([]buffer.Event{})

	if err != nil {
		t.Errorf("Expected no error for empty events, got: %v", err)
	}
}

func TestSendSuccess(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}

		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("Expected Content-Type: application/json")
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer test-key" {
			t.Errorf("Expected Authorization: Bearer test-key, got %s", authHeader)
		}

		// Parse request body
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("Failed to decode request body: %v", err)
		}

		// Verify events are present
		events, ok := payload["events"].([]interface{})
		if !ok || len(events) != 2 {
			t.Errorf("Expected 2 events in payload, got %v", events)
		}

		// Return success
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	}))
	defer server.Close()

	// Create forwarder
	f := New(server.URL, "test-key")

	// Send events
	events := []buffer.Event{
		{"id": "1", "service_name": "test"},
		{"id": "2", "service_name": "test"},
	}

	err := f.Send(events)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
}

func TestSendUnauthorized(t *testing.T) {
	// Create mock server that returns 401
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "Invalid API key"}`))
	}))
	defer server.Close()

	f := New(server.URL, "invalid-key")
	events := []buffer.Event{{"id": "1"}}

	err := f.Send(events)

	if err == nil {
		t.Error("Expected error for 401 response")
	}

	if err.Error() != "authentication failed: invalid API key" {
		t.Errorf("Expected authentication error, got: %v", err)
	}
}

func TestSendServerError(t *testing.T) {
	// Create mock server that returns 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "Server error"}`))
	}))
	defer server.Close()

	f := New(server.URL, "test-key")
	events := []buffer.Event{{"id": "1"}}

	err := f.Send(events)

	// Should retry and eventually fail
	if err == nil {
		t.Error("Expected error for 500 response after retries")
	}
}

func TestRetryableError(t *testing.T) {
	err := &RetryableError{Err: http.ErrServerClosed}

	if !isRetryable(err) {
		t.Error("Expected RetryableError to be retryable")
	}

	normalErr := http.ErrNotSupported
	if isRetryable(normalErr) {
		t.Error("Expected normal error to not be retryable")
	}
}
