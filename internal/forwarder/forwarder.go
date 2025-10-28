package forwarder

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yaat-app/sidecar/internal/buffer"
)

// Options configures Forwarder behaviour.
type Options struct {
	BatchSize     int
	Compress      bool
	MaxBatchBytes int
}

// Forwarder sends events to the YAAT API.
type Forwarder struct {
	apiEndpoint string
	apiKey      string
	client      *http.Client
	opts        Options
}

// TestReport captures the details of a connectivity test.
type TestReport struct {
	Endpoint string
	Events   []buffer.Event
	Latency  time.Duration
}

func defaultOptions() Options {
	return Options{
		BatchSize:     500,
		Compress:      false,
		MaxBatchBytes: 0,
	}
}

// New creates a Forwarder with default options.
func New(apiEndpoint, apiKey string) *Forwarder {
	return NewWithOptions(apiEndpoint, apiKey, Options{})
}

// NewWithOptions creates a Forwarder with explicit options.
func NewWithOptions(apiEndpoint, apiKey string, opts Options) *Forwarder {
	defaults := defaultOptions()
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaults.BatchSize
	}
	if opts.MaxBatchBytes < 0 {
		opts.MaxBatchBytes = defaults.MaxBatchBytes
	}

	return &Forwarder{
		apiEndpoint: apiEndpoint,
		apiKey:      apiKey,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		opts: opts,
	}
}

// SetHTTPClient allows tests and advanced callers to override the HTTP client used for delivery.
func (f *Forwarder) SetHTTPClient(client *http.Client) {
	if client == nil {
		return
	}
	f.client = client
}

// Send sends events to the YAAT API with retry logic.
func (f *Forwarder) Send(events []buffer.Event) error {
	if len(events) == 0 {
		return nil
	}

	chunks, err := f.partition(events)
	if err != nil {
		return err
	}

	for _, chunk := range chunks {
		if err := f.sendChunk(chunk); err != nil {
			return err
		}
	}

	return nil
}

func (f *Forwarder) sendChunk(events []buffer.Event) error {
	body, compressed, err := f.encodePayload(events)
	if err != nil {
		return err
	}

	if f.opts.MaxBatchBytes > 0 && len(body) > f.opts.MaxBatchBytes {
		log.Printf("[Forwarder] Warning: payload size %d bytes exceeds configured limit %d; sending anyway", len(body), f.opts.MaxBatchBytes)
	}

	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second
			log.Printf("[Forwarder] Retry attempt %d after %v", attempt+1, backoff)
			time.Sleep(backoff)
		}

		err = f.sendRequest(body, compressed)
		if err == nil {
			log.Printf("[Forwarder] Successfully sent %d events", len(events))
			return nil
		}

		if !isRetryable(err) {
			log.Printf("[Forwarder] Non-retryable error: %v", err)
			return err
		}

		log.Printf("[Forwarder] Retryable error (attempt %d/%d): %v", attempt+1, maxRetries, err)
	}

	return fmt.Errorf("failed after %d retries: %w", maxRetries, err)
}

func (f *Forwarder) partition(events []buffer.Event) ([][]buffer.Event, error) {
	now := time.Now().UTC()
	for i := range events {
		if err := normalizeEvent(events[i], now); err != nil {
			return nil, fmt.Errorf("event[%d] invalid: %w", i, err)
		}
	}

	var batches [][]buffer.Event
	for i := 0; i < len(events); {
		end := i + f.opts.BatchSize
		if end > len(events) {
			end = len(events)
		}

		for {
			chunk := events[i:end]
			raw, err := f.marshalEvents(chunk)
			if err != nil {
				return nil, err
			}

			if f.opts.MaxBatchBytes > 0 && len(raw) > f.opts.MaxBatchBytes && end-i > 1 {
				end--
				continue
			}

			batches = append(batches, chunk)
			i = end
			break
		}
	}

	return batches, nil
}

func (f *Forwarder) encodePayload(events []buffer.Event) ([]byte, bool, error) {
	raw, err := f.marshalEvents(events)
	if err != nil {
		return nil, false, err
	}

	if !f.opts.Compress {
		return raw, false, nil
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		return nil, false, fmt.Errorf("failed to gzip payload: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, false, fmt.Errorf("failed to finalize gzip payload: %w", err)
	}

	return buf.Bytes(), true, nil
}

func (f *Forwarder) marshalEvents(events []buffer.Event) ([]byte, error) {
	payload := map[string]interface{}{
		"events": events,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal events: %w", err)
	}
	return raw, nil
}

// sendRequest sends a single HTTP request.
func (f *Forwarder) sendRequest(body []byte, compressed bool) error {
	req, err := http.NewRequest("POST", f.apiEndpoint, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", f.apiKey))
	if compressed {
		req.Header.Set("Content-Encoding", "gzip")
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return &RetryableError{Err: err}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case 200, 201:
		return nil
	case 401:
		return fmt.Errorf("authentication failed: invalid API key")
	case 429:
		return &RetryableError{Err: fmt.Errorf("rate limited")}
	case 500, 502, 503, 504:
		return &RetryableError{Err: fmt.Errorf("server error: %d - %s", resp.StatusCode, string(respBody))}
	default:
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
}

// RetryableError represents an error that can be retried.
type RetryableError struct {
	Err error
}

func (e *RetryableError) Error() string {
	return e.Err.Error()
}

// isRetryable checks if an error is retryable.
func isRetryable(err error) bool {
	_, ok := err.(*RetryableError)
	return ok
}

// Test sends a curated batch of test events to validate connectivity.
func (f *Forwarder) Test(serviceName, environment string) (*TestReport, error) {
	if serviceName == "" {
		serviceName = "yaat-sidecar"
	}
	if environment == "" {
		environment = "production"
	}

	events := makeTestEvents(serviceName, environment)
	start := time.Now()

	if err := f.Send(events); err != nil {
		return nil, err
	}

	return &TestReport{
		Endpoint: f.apiEndpoint,
		Events:   cloneEvents(events),
		Latency:  time.Since(start),
	}, nil
}

func makeTestEvents(serviceName, environment string) []buffer.Event {
	now := time.Now().UTC()

	logEvent := buffer.Event{
		"service_name": serviceName,
		"event_id":     uuid.NewString(),
		"timestamp":    now.Add(-2 * time.Second).Format(time.RFC3339Nano),
		"event_type":   "log",
		"environment":  environment,
		"level":        "info",
		"message":      "YAAT Sidecar connectivity test",
		"tags": map[string]string{
			"yaat.sidecar": "true",
			"yaat.test":    "true",
			"category":     "connectivity",
		},
	}

	spanEvent := buffer.Event{
		"service_name":   serviceName,
		"event_id":       uuid.NewString(),
		"timestamp":      now.Add(-1 * time.Second).Format(time.RFC3339Nano),
		"event_type":     "span",
		"environment":    environment,
		"trace_id":       uuid.NewString(),
		"span_id":        uuid.NewString(),
		"parent_span_id": "",
		"operation":      "GET /yaat/healthz",
		"duration_ms":    128.4,
		"status_code":    200,
		"tags": map[string]string{
			"yaat.sidecar": "true",
			"yaat.test":    "true",
			"component":    "proxy",
			"endpoint":     "/yaat/healthz",
		},
	}

	metricEvent := buffer.Event{
		"service_name": serviceName,
		"event_id":     uuid.NewString(),
		"timestamp":    now.Format(time.RFC3339Nano),
		"event_type":   "metric",
		"environment":  environment,
		"metric_name":  "yaat.sidecar.test.latency_ms",
		"metric_value": 128.4,
		"tags": map[string]string{
			"yaat.sidecar": "true",
			"yaat.test":    "true",
			"unit":         "milliseconds",
		},
	}

	return []buffer.Event{logEvent, spanEvent, metricEvent}
}

func cloneEvents(events []buffer.Event) []buffer.Event {
	cloned := make([]buffer.Event, len(events))
	for i, evt := range events {
		copyEvent := make(buffer.Event, len(evt))
		for k, v := range evt {
			switch val := v.(type) {
			case map[string]string:
				tagsCopy := make(map[string]string, len(val))
				for tk, tv := range val {
					tagsCopy[tk] = tv
				}
				copyEvent[k] = tagsCopy
			case map[string]interface{}:
				tagsCopy := make(map[string]interface{}, len(val))
				for tk, tv := range val {
					tagsCopy[tk] = tv
				}
				copyEvent[k] = tagsCopy
			default:
				copyEvent[k] = val
			}
		}
		cloned[i] = copyEvent
	}
	return cloned
}

func normalizeEvent(evt buffer.Event, now time.Time) error {
	serviceName := strings.TrimSpace(getString(evt, "service_name"))
	if serviceName == "" {
		return fmt.Errorf("service_name is required")
	}
	evt["service_name"] = serviceName

	if _, ok := evt["event_id"]; !ok || strings.TrimSpace(getString(evt, "event_id")) == "" {
		evt["event_id"] = uuid.NewString()
	}

	environment := strings.TrimSpace(getString(evt, "environment"))
	if environment == "" {
		environment = "production"
	}
	evt["environment"] = environment

	eventType := strings.TrimSpace(strings.ToLower(getString(evt, "event_type")))
	if eventType == "" {
		eventType = "log"
	}
	if _, ok := map[string]struct{}{"log": {}, "span": {}, "metric": {}}[eventType]; !ok {
		return fmt.Errorf("invalid event_type %q", eventType)
	}
	evt["event_type"] = eventType

	level := strings.TrimSpace(strings.ToLower(getString(evt, "level")))
	evt["level"] = level

	ts, err := normalizeTimestamp(evt["timestamp"], now)
	if err != nil {
		return fmt.Errorf("timestamp: %w", err)
	}
	evt["timestamp"] = ts.Format(time.RFC3339Nano)
	evt["received_at"] = now.Format(time.RFC3339Nano)

	if err := normalizeTags(evt); err != nil {
		return fmt.Errorf("tags: %w", err)
	}

	return nil
}

func normalizeTimestamp(value interface{}, fallback time.Time) (time.Time, error) {
	if value == nil {
		return fallback, nil
	}

	switch v := value.(type) {
	case time.Time:
		return v.UTC(), nil
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return fallback, nil
		}
		if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
			return parsed.UTC(), nil
		}
		if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
			return parsed.UTC(), nil
		}
		return fallback, fmt.Errorf("invalid string %q (expected RFC3339)", v)
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return unixFloatToTime(f), nil
		}
	case float64:
		return unixFloatToTime(v), nil
	case float32:
		return unixFloatToTime(float64(v)), nil
	case int64:
		return time.Unix(v, 0).UTC(), nil
	case int:
		return time.Unix(int64(v), 0).UTC(), nil
	}

	return fallback, fmt.Errorf("unsupported type %T", value)
}

func unixFloatToTime(val float64) time.Time {
	seconds := int64(val)
	nanos := int64((val - float64(seconds)) * float64(time.Second))
	return time.Unix(seconds, nanos).UTC()
}

func normalizeTags(evt buffer.Event) error {
	raw, ok := evt["tags"]
	if !ok || raw == nil {
		evt["tags"] = map[string]string{}
		return nil
	}

	switch tags := raw.(type) {
	case map[string]string:
		evt["tags"] = tags
	case map[string]interface{}:
		converted := make(map[string]string, len(tags))
		for k, v := range tags {
			converted[k] = fmt.Sprint(v)
		}
		evt["tags"] = converted
	default:
		return fmt.Errorf("unsupported type %T", raw)
	}
	return nil
}

func getString(evt buffer.Event, key string) string {
	val, ok := evt[key]
	if !ok || val == nil {
		return ""
	}

	switch v := val.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case json.Number:
		return v.String()
	case int:
		return strconv.FormatInt(int64(v), 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 64)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}
