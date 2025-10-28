package forwarder

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/yaat-app/sidecar/internal/buffer"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

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
	f := New("https://example.test/ingest", "test-key")

	f.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost {
				t.Fatalf("expected POST request, got %s", req.Method)
			}
			if req.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("expected Content-Type application/json, got %s", req.Header.Get("Content-Type"))
			}
			if req.Header.Get("Authorization") != "Bearer test-key" {
				t.Fatalf("unexpected Authorization header: %s", req.Header.Get("Authorization"))
			}

			payload, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			_ = req.Body.Close()

			var decoded map[string]interface{}
			if err := json.Unmarshal(payload, &decoded); err != nil {
				return nil, err
			}
			if events, ok := decoded["events"].([]interface{}); !ok || len(events) != 2 {
				t.Fatalf("expected 2 events, got %v", decoded["events"])
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"status":"ok"}`))),
			}, nil
		}),
	})

	events := []buffer.Event{
		{"id": "1", "service_name": "test"},
		{"id": "2", "service_name": "test"},
	}

	if err := f.Send(events); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestSendUnauthorized(t *testing.T) {
	f := New("https://example.test/ingest", "invalid-key")

	f.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"error":"Invalid API key"}`))),
			}, nil
		}),
	})
	events := []buffer.Event{{"id": "1", "service_name": "test"}}

	err := f.Send(events)

	if err == nil {
		t.Error("Expected error for 401 response")
	}

	if err.Error() != "authentication failed: invalid API key" {
		t.Errorf("Expected authentication error, got: %v", err)
	}
}

func TestSendServerError(t *testing.T) {
	f := New("https://example.test/ingest", "test-key")
	f.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"error":"Server error"}`))),
			}, nil
		}),
	})
	events := []buffer.Event{{"id": "1", "service_name": "test"}}

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
