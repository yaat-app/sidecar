package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yaat-app/sidecar/internal/buffer"
)

const (
	stateDirName        = ".yaat"
	stateFileName       = "state.json"
	maxStoredTestEvents = 20
)

// State represents persisted UI state for the sidecar.
type State struct {
	ConfigPath  string     `json:"config_path"`
	LastSetupAt time.Time  `json:"last_setup_at"`
	LastTest    TestResult `json:"last_test"`
}

// TestResult captures the outcome of the last connectivity test.
type TestResult struct {
	RanAt         time.Time   `json:"ran_at"`
	Success       bool        `json:"success"`
	Endpoint      string      `json:"endpoint"`
	ServiceName   string      `json:"service_name,omitempty"`
	Environment   string      `json:"environment,omitempty"`
	LatencyMillis int64       `json:"latency_ms"`
	Error         string      `json:"error,omitempty"`
	Events        []TestEvent `json:"events,omitempty"`
}

// TestEvent represents a simplified view of an event that was sent during a test.
type TestEvent struct {
	EventID     string            `json:"event_id"`
	Timestamp   time.Time         `json:"timestamp"`
	EventType   string            `json:"event_type"`
	Level       string            `json:"level,omitempty"`
	Message     string            `json:"message,omitempty"`
	Stacktrace  string            `json:"stacktrace,omitempty"`
	TraceID     string            `json:"trace_id,omitempty"`
	SpanID      string            `json:"span_id,omitempty"`
	Operation   string            `json:"operation,omitempty"`
	StatusCode  int               `json:"status_code,omitempty"`
	DurationMs  float64           `json:"duration_ms,omitempty"`
	MetricName  string            `json:"metric_name,omitempty"`
	MetricValue float64           `json:"metric_value,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
}

// Load reads the persisted state from disk. If no state is present a new instance is returned.
func Load() (*State, error) {
	path, err := stateFilePath()
	if err != nil {
		return &State{}, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &State{}, nil
	}
	if err != nil {
		return &State{}, fmt.Errorf("read state: %w", err)
	}

	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return &State{}, fmt.Errorf("parse state: %w", err)
	}

	return &st, nil
}

// Save writes the state file to disk.
func Save(st *State) error {
	if st == nil {
		return fmt.Errorf("state is nil")
	}

	path, err := stateFilePath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}

	return nil
}

// Update loads the current state, applies the mutator, and persists it.
func Update(mutator func(*State)) error {
	st, err := Load()
	if err != nil {
		return err
	}

	mutator(st)
	return Save(st)
}

// RecordConfig persists the latest configuration path and timestamp.
func RecordConfig(configPath string) error {
	if configPath == "" {
		return fmt.Errorf("config path is empty")
	}
	return Update(func(st *State) {
		st.ConfigPath = configPath
		st.LastSetupAt = time.Now().UTC()
	})
}

// RecordTestOutcome builds and saves a test result from the provided data.
func RecordTestOutcome(endpoint, serviceName, environment string, events []buffer.Event, latency time.Duration, testErr error) error {
	result := NewTestResult(endpoint, serviceName, environment, events, latency, testErr)
	return RecordTest(result)
}

// RecordTest stores the provided test result.
func RecordTest(result TestResult) error {
	return Update(func(st *State) {
		// Ensure the newest timestamp is always set, even on failure.
		if result.RanAt.IsZero() {
			result.RanAt = time.Now().UTC()
		}
		if len(result.Events) > maxStoredTestEvents {
			result.Events = result.Events[:maxStoredTestEvents]
		}
		st.LastTest = result
	})
}

// NewTestResult creates a TestResult helper instance.
func NewTestResult(endpoint, serviceName, environment string, events []buffer.Event, latency time.Duration, testErr error) TestResult {
	result := TestResult{
		RanAt:         time.Now().UTC(),
		Success:       testErr == nil,
		Endpoint:      endpoint,
		ServiceName:   serviceName,
		Environment:   environment,
		LatencyMillis: int64(latency / time.Millisecond),
	}
	if testErr != nil {
		result.Error = testErr.Error()
	}
	if len(events) > 0 {
		result.Events = FromBufferEvents(events, maxStoredTestEvents)
	}
	return result
}

// FromBufferEvents converts buffer events into TestEvent types for persistence.
func FromBufferEvents(events []buffer.Event, limit int) []TestEvent {
	if limit <= 0 || len(events) == 0 {
		return nil
	}

	if len(events) > limit {
		events = events[:limit]
	}

	out := make([]TestEvent, 0, len(events))
	for _, evt := range events {
		out = append(out, fromBufferEvent(evt))
	}
	return out
}

func fromBufferEvent(evt buffer.Event) TestEvent {
	getString := func(key string) string {
		if val, ok := evt[key]; ok {
			switch v := val.(type) {
			case string:
				return v
			}
		}
		return ""
	}

	getFloat := func(key string) float64 {
		if val, ok := evt[key]; ok {
			switch v := val.(type) {
			case float64:
				return v
			case int:
				return float64(v)
			case int64:
				return float64(v)
			case float32:
				return float64(v)
			}
		}
		return 0
	}

	getInt := func(key string) int {
		if val, ok := evt[key]; ok {
			switch v := val.(type) {
			case int:
				return v
			case int64:
				return int(v)
			case float64:
				return int(v)
			case json.Number:
				i, _ := v.Int64()
				return int(i)
			}
		}
		return 0
	}

	timestamp := time.Now().UTC()
	switch val := evt["timestamp"].(type) {
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, val); err == nil {
			timestamp = parsed
		} else if parsed, err := time.Parse(time.RFC3339, val); err == nil {
			timestamp = parsed
		}
	case time.Time:
		timestamp = val
	}

	testEvent := TestEvent{
		EventID:     getString("event_id"),
		Timestamp:   timestamp,
		EventType:   strings.ToLower(getString("event_type")),
		Level:       strings.ToLower(getString("level")),
		Message:     getString("message"),
		Stacktrace:  getString("stacktrace"),
		TraceID:     getString("trace_id"),
		SpanID:      getString("span_id"),
		Operation:   getString("operation"),
		StatusCode:  getInt("status_code"),
		DurationMs:  getFloat("duration_ms"),
		MetricName:  getString("metric_name"),
		MetricValue: getFloat("metric_value"),
	}

	if rawTags, ok := evt["tags"]; ok {
		tags := map[string]string{}
		switch val := rawTags.(type) {
		case map[string]string:
			for k, v := range val {
				tags[k] = v
			}
		case map[string]interface{}:
			for k, v := range val {
				switch vv := v.(type) {
				case string:
					tags[k] = vv
				case fmt.Stringer:
					tags[k] = vv.String()
				case int:
					tags[k] = fmt.Sprintf("%d", vv)
				case float64:
					tags[k] = fmt.Sprintf("%v", vv)
				case bool:
					tags[k] = fmt.Sprintf("%t", vv)
				default:
					tags[k] = fmt.Sprintf("%v", vv)
				}
			}
		}
		if len(tags) > 0 {
			testEvent.Tags = tags
		}
	}

	return testEvent
}

func stateFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, stateDirName, stateFileName), nil
}
