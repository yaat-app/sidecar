package logs

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yaat/sidecar/internal/buffer"
)

// DjangoLogParser parses Django log format
// Format: [2024-10-26 10:30:15,123] ERROR [django.request] Message here
var djangoLogRegex = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2},\d{3})\] (\w+) \[([^\]]+)\] (.+)$`)

// NginxLogParser parses Nginx access log format
// Format: IP - - [timestamp] "METHOD /path HTTP/1.1" status size
var nginxLogRegex = regexp.MustCompile(`^(\S+) - - \[([^\]]+)\] "(\w+) ([^ ]+) HTTP/[^"]+" (\d+) (\d+)`)

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
	// timestamp := matches[2]
	method := matches[3]
	path := matches[4]
	statusStr := matches[5]
	sizeStr := matches[6]

	status, _ := strconv.Atoi(statusStr)
	size, _ := strconv.Atoi(sizeStr)

	// Create span event for HTTP request
	return &buffer.Event{
		"service_name": serviceName,
		"event_id":     uuid.New().String(),
		"timestamp":    time.Now().UTC().Format(time.RFC3339),
		"event_type":   "span",
		"environment":  environment,
		"trace_id":     uuid.New().String(),
		"span_id":      uuid.New().String(),
		"operation":    method + " " + path,
		"duration_ms":  0.0, // Not available from access logs
		"status_code":  status,
		"tags": map[string]string{
			"method":       method,
			"path":         path,
			"client_ip":    ip,
			"content_size": sizeStr,
		},
		"metric_value": float64(size),
	}
}

// ParseJSONLog parses a JSON log line
func ParseJSONLog(line, serviceName, environment string) *buffer.Event {
	// For now, treat as generic log
	// In a real implementation, you'd parse the JSON
	return &buffer.Event{
		"service_name": serviceName,
		"event_id":     uuid.New().String(),
		"timestamp":    time.Now().UTC().Format(time.RFC3339),
		"event_type":   "log",
		"environment":  environment,
		"level":        "info",
		"message":      line,
	}
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
	case "json":
		return ParseJSONLog(line, serviceName, environment)
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
		}
	}
}
