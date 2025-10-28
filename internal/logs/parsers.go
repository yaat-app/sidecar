package logs

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yaat-app/sidecar/internal/buffer"
)

// DjangoLogParser parses Django log format
// Format: [2024-10-26 10:30:15,123] ERROR [django.request] Message here
var djangoLogRegex = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2},\d{3})\] (\w+) \[([^\]]+)\] (.+)$`)

// NginxLogParser parses Nginx access log format
// Format: IP - - [timestamp] "METHOD /path HTTP/1.1" status size "referer" "user-agent"
var nginxLogRegex = regexp.MustCompile(`^(\S+) - - \[([^\]]+)\] "(\w+) ([^ ]+) HTTP/[^"]+" (\d+) (\d+)(?: "([^"]*)" "([^"]*)")?`)

// ApacheLogParser parses Apache Common/Combined log format
// Format: IP - - [timestamp] "METHOD /path HTTP/1.1" status size "referer" "user-agent"
var apacheLogRegex = regexp.MustCompile(`^(\S+) - - \[([^\]]+)\] "(\w+) ([^ ]+) HTTP/[^"]+" (\d+) (\d+)(?: "([^"]*)" "([^"]*)")?`)

// ParseDjangoLog parses a Django log line
func ParseDjangoLog(line, serviceName, environment string) *buffer.Event {
	matches := djangoLogRegex.FindStringSubmatch(line)
	if matches == nil {
		// If it doesn't match, treat as generic log
		return &buffer.Event{
			"service_name": serviceName,
			"event_id":     uuid.New().String(),
			"timestamp":    time.Now().UTC().Format(time.RFC3339),
			"event_type":   "log",
			"environment":  environment,
			"level":        "info",
			"message":      line,
			"stacktrace":   "",
		}
	}

	// Extract fields
	timestamp := matches[1]
	level := strings.ToLower(matches[2])
	logger := matches[3]
	message := matches[4]

	// Parse timestamp
	t, err := time.Parse("2006-01-02 15:04:05,000", timestamp)
	if err != nil {
		t = time.Now().UTC()
	}

	// Map Django log levels to standard levels
	logLevel := mapLogLevel(level)

	return &buffer.Event{
		"service_name": serviceName,
		"event_id":     uuid.New().String(),
		"timestamp":    t.UTC().Format(time.RFC3339),
		"event_type":   "log",
		"environment":  environment,
		"level":        logLevel,
		"message":      message,
		"stacktrace":   "",
		"tags": map[string]string{
			"logger": logger,
		},
	}
}

// ParseNginxLog parses an Nginx access log line
func ParseNginxLog(line, serviceName, environment string) *buffer.Event {
	matches := nginxLogRegex.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	// Extract fields
	ip := matches[1]
	timestamp := matches[2]
	method := matches[3]
	path := matches[4]
	statusStr := matches[5]
	sizeStr := matches[6]
	referer := ""
	userAgent := ""

	// Optional fields (referer and user-agent)
	if len(matches) > 7 && matches[7] != "" {
		referer = matches[7]
	}
	if len(matches) > 8 && matches[8] != "" {
		userAgent = matches[8]
	}

	status, _ := strconv.Atoi(statusStr)
	size, _ := strconv.Atoi(sizeStr)

	// Parse timestamp (Apache/Nginx format: 02/Jan/2006:15:04:05 -0700)
	parsedTime, err := time.Parse("02/Jan/2006:15:04:05 -0700", timestamp)
	if err != nil {
		parsedTime = time.Now().UTC()
	}

	// Build tags
	tags := map[string]string{
		"method":       method,
		"path":         path,
		"client_ip":    ip,
		"content_size": sizeStr,
	}
	if referer != "" && referer != "-" {
		tags["referer"] = referer
	}
	if userAgent != "" && userAgent != "-" {
		tags["user_agent"] = userAgent
	}

	// Create span event for HTTP request
	return &buffer.Event{
		"service_name":   serviceName,
		"event_id":       uuid.New().String(),
		"timestamp":      parsedTime.UTC().Format(time.RFC3339),
		"event_type":     "span",
		"environment":    environment,
		"trace_id":       uuid.New().String(),
		"span_id":        uuid.New().String(),
		"parent_span_id": "", // Not available from access logs
		"operation":      method + " " + path,
		"duration_ms":    0.0, // Not available from access logs
		"status_code":    status,
		"tags":           tags,
		"metric_value":   float64(size),
	}
}

// ParseApacheLog parses an Apache access log line (Common/Combined format)
func ParseApacheLog(line, serviceName, environment string) *buffer.Event {
	// Apache format is very similar to Nginx, use same regex
	matches := apacheLogRegex.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	// Extract fields
	ip := matches[1]
	timestamp := matches[2]
	method := matches[3]
	path := matches[4]
	statusStr := matches[5]
	sizeStr := matches[6]
	referer := ""
	userAgent := ""

	// Optional fields
	if len(matches) > 7 && matches[7] != "" {
		referer = matches[7]
	}
	if len(matches) > 8 && matches[8] != "" {
		userAgent = matches[8]
	}

	status, _ := strconv.Atoi(statusStr)
	size, _ := strconv.Atoi(sizeStr)
	if sizeStr == "-" {
		size = 0
	}

	// Parse timestamp
	parsedTime, err := time.Parse("02/Jan/2006:15:04:05 -0700", timestamp)
	if err != nil {
		parsedTime = time.Now().UTC()
	}

	// Build tags
	tags := map[string]string{
		"method":       method,
		"path":         path,
		"client_ip":    ip,
		"content_size": sizeStr,
	}
	if referer != "" && referer != "-" {
		tags["referer"] = referer
	}
	if userAgent != "" && userAgent != "-" {
		tags["user_agent"] = userAgent
	}

	return &buffer.Event{
		"service_name":   serviceName,
		"event_id":       uuid.New().String(),
		"timestamp":      parsedTime.UTC().Format(time.RFC3339),
		"event_type":     "span",
		"environment":    environment,
		"trace_id":       uuid.New().String(),
		"span_id":        uuid.New().String(),
		"parent_span_id": "",
		"operation":      method + " " + path,
		"duration_ms":    0.0,
		"status_code":    status,
		"tags":           tags,
		"metric_value":   float64(size),
	}
}

// ParseDockerLog parses Docker/container runtime JSON log envelope lines.
func ParseDockerLog(line, serviceName, environment string) *buffer.Event {
	type dockerEnvelope struct {
		Log       string `json:"log"`
		Stream    string `json:"stream"`
		Time      string `json:"time"`
		TimeNano  int64  `json:"timeNano"`
		Source    string `json:"source"`
		Container string `json:"containerID"`
	}

	var env dockerEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		// Fall back to generic JSON parser; the payload might already be application JSON.
		return ParseJSONLog(line, serviceName, environment)
	}

	message := strings.TrimRight(env.Log, "\r\n")
	if message == "" {
		message = env.Log
	}

	timestamp := time.Now().UTC()
	if env.Time != "" {
		for _, format := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000000000Z07:00"} {
			if parsed, err := time.Parse(format, env.Time); err == nil {
				timestamp = parsed.UTC()
				break
			}
		}
	} else if env.TimeNano > 0 {
		timestamp = time.Unix(0, env.TimeNano).UTC()
	}

	stream := strings.ToLower(env.Stream)
	level := "info"
	if stream == "stderr" {
		level = "error"
	} else if stream == "stdout" {
		level = "info"
	}

	trimmed := strings.TrimSpace(message)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		if inner := ParseJSONLog(trimmed, serviceName, environment); inner != nil {
			(*inner)["timestamp"] = timestamp.Format(time.RFC3339Nano)
			(*inner)["event_id"] = uuid.New().String()
			tags := map[string]string{
				"container.stream":  stream,
				"container.runtime": "docker",
			}
			if env.Container != "" {
				tags["container.id"] = env.Container
			}
			if env.Source != "" {
				tags["container.source"] = env.Source
			}
			if existing, ok := (*inner)["tags"].(map[string]string); ok && len(existing) > 0 {
				merged := make(map[string]string, len(existing)+len(tags))
				for k, v := range existing {
					merged[k] = v
				}
				for k, v := range tags {
					merged[k] = v
				}
				tags = merged
			}
			(*inner)["tags"] = tags
			if stream == "stderr" {
				if current, ok := (*inner)["level"].(string); !ok || current == "" || current == "info" {
					(*inner)["level"] = "error"
				}
			}
			return inner
		}
	}

	event := buffer.Event{
		"service_name": serviceName,
		"event_id":     uuid.New().String(),
		"timestamp":    timestamp.Format(time.RFC3339Nano),
		"event_type":   "log",
		"environment":  environment,
		"level":        level,
		"message":      message,
	}

	tags := map[string]string{
		"container.stream":  stream,
		"container.runtime": "docker",
	}
	if env.Container != "" {
		tags["container.id"] = env.Container
	}
	if env.Source != "" {
		tags["container.source"] = env.Source
	}
	if len(tags) > 0 {
		event["tags"] = tags
	}

	return &event
}

// ParseJSONLog parses a JSON log line
func ParseJSONLog(line, serviceName, environment string) *buffer.Event {
	// Try to parse as JSON
	var logData map[string]interface{}
	if err := json.Unmarshal([]byte(line), &logData); err != nil {
		// Not valid JSON, treat as generic log
		return &buffer.Event{
			"service_name": serviceName,
			"event_id":     uuid.New().String(),
			"timestamp":    time.Now().UTC().Format(time.RFC3339),
			"event_type":   "log",
			"environment":  environment,
			"level":        "info",
			"message":      line,
			"stacktrace":   "",
		}
	}

	// Extract common fields from JSON
	level := "info"
	message := line
	stacktrace := ""
	timestamp := time.Now().UTC().Format(time.RFC3339)

	// Try to extract level (various common field names)
	for _, key := range []string{"level", "severity", "log_level", "loglevel"} {
		if val, ok := logData[key]; ok {
			if str, ok := val.(string); ok {
				level = mapLogLevel(str)
				break
			}
		}
	}

	// Try to extract message
	for _, key := range []string{"message", "msg", "text", "log"} {
		if val, ok := logData[key]; ok {
			if str, ok := val.(string); ok {
				message = str
				break
			}
		}
	}

	// Try to extract timestamp
	for _, key := range []string{"timestamp", "time", "@timestamp", "ts"} {
		if val, ok := logData[key]; ok {
			if str, ok := val.(string); ok {
				// Try to parse various timestamp formats
				for _, format := range []string{
					time.RFC3339,
					time.RFC3339Nano,
					"2006-01-02T15:04:05.000Z07:00",
					"2006-01-02 15:04:05",
				} {
					if t, err := time.Parse(format, str); err == nil {
						timestamp = t.UTC().Format(time.RFC3339)
						break
					}
				}
				break
			}
		}
	}

	// Try to extract stack trace
	for _, key := range []string{"stacktrace", "stack_trace", "stack", "trace"} {
		if val, ok := logData[key]; ok {
			if str, ok := val.(string); ok {
				stacktrace = str
				break
			}
		}
	}

	// Build tags from remaining fields
	tags := make(map[string]string)
	for key, val := range logData {
		// Skip fields we've already extracted
		if key == "level" || key == "severity" || key == "log_level" || key == "loglevel" ||
			key == "message" || key == "msg" || key == "text" || key == "log" ||
			key == "timestamp" || key == "time" || key == "@timestamp" || key == "ts" ||
			key == "stacktrace" || key == "stack_trace" || key == "stack" || key == "trace" {
			continue
		}

		// Convert value to string
		if str, ok := val.(string); ok {
			tags[key] = str
		} else if num, ok := val.(float64); ok {
			tags[key] = strconv.FormatFloat(num, 'f', -1, 64)
		} else if b, ok := val.(bool); ok {
			tags[key] = strconv.FormatBool(b)
		}
	}

	event := &buffer.Event{
		"service_name": serviceName,
		"event_id":     uuid.New().String(),
		"timestamp":    timestamp,
		"event_type":   "log",
		"environment":  environment,
		"level":        level,
		"message":      message,
		"stacktrace":   stacktrace,
	}

	// Add tags if any
	if len(tags) > 0 {
		(*event)["tags"] = tags
	}

	return event
}

// mapLogLevel maps various log level strings to standard levels
func mapLogLevel(level string) string {
	level = strings.ToLower(level)
	switch level {
	case "debug":
		return "debug"
	case "info", "information":
		return "info"
	case "warn", "warning":
		return "warning"
	case "error", "err":
		return "error"
	case "fatal", "critical":
		return "critical"
	default:
		return "info"
	}
}

// ParseLog parses a log line based on format
func ParseLog(line, format, serviceName, environment string) *buffer.Event {
	switch format {
	case "django":
		return ParseDjangoLog(line, serviceName, environment)
	case "nginx":
		return ParseNginxLog(line, serviceName, environment)
	case "apache":
		return ParseApacheLog(line, serviceName, environment)
	case "json":
		return ParseJSONLog(line, serviceName, environment)
	case "docker":
		return ParseDockerLog(line, serviceName, environment)
	default:
		// Generic log
		return &buffer.Event{
			"service_name": serviceName,
			"event_id":     uuid.New().String(),
			"timestamp":    time.Now().UTC().Format(time.RFC3339),
			"event_type":   "log",
			"environment":  environment,
			"level":        "info",
			"message":      line,
			"stacktrace":   "",
		}
	}
}
