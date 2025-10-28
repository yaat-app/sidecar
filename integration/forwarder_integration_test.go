package integration

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/yaat-app/sidecar/internal/buffer"
	"github.com/yaat-app/sidecar/internal/forwarder"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type ingestPayload struct {
	Events []map[string]interface{} `json:"events"`
}

func TestForwarderSend_NormalizesAndPosts(t *testing.T) {
	var captured ingestPayload
	var authHeader string

	fwd := forwarder.NewWithOptions("https://example.test/ingest", "test-key", forwarder.Options{
		BatchSize:     10,
		Compress:      false,
		MaxBatchBytes: 0,
	})

	fwd.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			authHeader = req.Header.Get("Authorization")
			payload, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			_ = req.Body.Close()

			if err := json.Unmarshal(payload, &captured); err != nil {
				return nil, err
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"status":"ok"}`))),
				Header:     make(http.Header),
			}, nil
		}),
	})

	err := fwd.Send([]buffer.Event{
		{
			"service_name": "demo-service",
			"event_type":   "log",
			"message":      "hello",
			"tags": map[string]interface{}{
				"env": "testing",
			},
		},
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	if authHeader != "Bearer test-key" {
		t.Fatalf("expected Authorization header, got %q", authHeader)
	}

	if len(captured.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(captured.Events))
	}

	event := captured.Events[0]

	if event["service_name"] != "demo-service" {
		t.Fatalf("service_name mismatch: %+v", event["service_name"])
	}
	if event["event_id"] == "" {
		t.Fatalf("expected event_id to be populated")
	}
	if event["timestamp"] == "" {
		t.Fatalf("expected timestamp to be populated")
	}
	if event["received_at"] == "" {
		t.Fatalf("expected received_at to be populated")
	}
	if _, ok := event["tags"]; !ok {
		t.Fatalf("expected tags to be present")
	}
}

func TestForwarderSend_MissingServiceNameFails(t *testing.T) {
	fwd := forwarder.NewWithOptions("https://example.test/ingest", "test-key", forwarder.Options{
		BatchSize:     10,
		Compress:      false,
		MaxBatchBytes: 0,
	})

	called := false
	fwd.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			called = true
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil))}, nil
		}),
	})
	err := fwd.Send([]buffer.Event{
		{
			"message": "no service name",
		},
	})
	if err == nil {
		t.Fatalf("expected error for missing service_name")
	}
	if called {
		t.Fatalf("request should not be sent when validation fails")
	}
}

func TestForwarderSend_RespectsBatchSize(t *testing.T) {
	var requests int
	fwd := forwarder.NewWithOptions("https://example.test/ingest", "test-key", forwarder.Options{
		BatchSize:     1,
		Compress:      false,
		MaxBatchBytes: 0,
	})

	fwd.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil))}, nil
		}),
	})

	err := fwd.Send([]buffer.Event{
		{"service_name": "demo", "event_type": "log", "message": "one"},
		{"service_name": "demo", "event_type": "log", "message": "two"},
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	if requests != 2 {
		t.Fatalf("expected 2 requests, got %d", requests)
	}
}

func TestForwarderSend_Compression(t *testing.T) {
	var encoding string
	fwd := forwarder.NewWithOptions("https://example.test/ingest", "test-key", forwarder.Options{
		BatchSize:     10,
		Compress:      true,
		MaxBatchBytes: 0,
	})

	fwd.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			encoding = req.Header.Get("Content-Encoding")
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			_ = req.Body.Close()
			if encoding != "gzip" {
				return nil, fmt.Errorf("unexpected encoding: %s", encoding)
			}
			if _, err := gzip.NewReader(bytes.NewReader(body)); err != nil {
				return nil, err
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil))}, nil
		}),
	})

	if err := fwd.Send([]buffer.Event{{"service_name": "demo", "event_type": "log", "message": "compressed"}}); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	if encoding != "gzip" {
		t.Fatalf("expected gzip content encoding, got %q", encoding)
	}
}
