package logs

import (
	"testing"
)

func TestParseDjangoLogValid(t *testing.T) {
	line := "[2024-10-26 10:30:15,123] ERROR [django.request] Internal server error"
	serviceName := "my-service"
	environment := "production"

	event := ParseDjangoLog(line, serviceName, environment)

	if event == nil {
		t.Fatal("ParseDjangoLog returned nil for valid log")
	}

	// Check required fields
	if (*event)["service_name"] != serviceName {
		t.Errorf("Expected service_name '%s', got '%v'", serviceName, (*event)["service_name"])
	}

	if (*event)["environment"] != environment {
		t.Errorf("Expected environment '%s', got '%v'", environment, (*event)["environment"])
	}

	if (*event)["event_type"] != "log" {
		t.Errorf("Expected event_type 'log', got '%v'", (*event)["event_type"])
	}

	if (*event)["level"] != "error" {
		t.Errorf("Expected level 'error', got '%v'", (*event)["level"])
	}

	if (*event)["message"] != "Internal server error" {
		t.Errorf("Expected message 'Internal server error', got '%v'", (*event)["message"])
	}

	// Check tags
	tags, ok := (*event)["tags"].(map[string]string)
	if !ok {
		t.Error("Expected tags to be map[string]string")
	} else if tags["logger"] != "django.request" {
		t.Errorf("Expected logger tag 'django.request', got '%s'", tags["logger"])
	}
}

func TestParseDjangoLogInvalid(t *testing.T) {
	line := "This is not a valid Django log line"
	serviceName := "my-service"
	environment := "production"

	event := ParseDjangoLog(line, serviceName, environment)

	if event == nil {
		t.Fatal("ParseDjangoLog returned nil for invalid log (should return generic event)")
	}

	// Should create generic log event
	if (*event)["service_name"] != serviceName {
		t.Errorf("Expected service_name '%s', got '%v'", serviceName, (*event)["service_name"])
	}

	if (*event)["level"] != "info" {
		t.Errorf("Expected default level 'info', got '%v'", (*event)["level"])
	}

	if (*event)["message"] != line {
		t.Errorf("Expected message to be the original line, got '%v'", (*event)["message"])
	}
}

func TestParseDjangoLogDifferentLevels(t *testing.T) {
	tests := []struct {
		logLevel     string
		expectedLevel string
	}{
		{"DEBUG", "debug"},
		{"INFO", "info"},
		{"WARNING", "warning"},
		{"ERROR", "error"},
		{"CRITICAL", "critical"},
	}

	for _, tt := range tests {
		t.Run(tt.logLevel, func(t *testing.T) {
			line := "[2024-10-26 10:30:15,123] " + tt.logLevel + " [django.test] Test message"
			event := ParseDjangoLog(line, "test", "production")

			if event == nil {
				t.Fatal("ParseDjangoLog returned nil")
			}

			level := (*event)["level"]
			if level != tt.expectedLevel {
				t.Errorf("Expected level '%s', got '%v'", tt.expectedLevel, level)
			}
		})
	}
}

func TestParseNginxLogValid(t *testing.T) {
	line := `192.168.1.1 - - [26/Oct/2024:10:30:15 +0000] "GET /api/users HTTP/1.1" 200 1234`
	serviceName := "nginx-service"
	environment := "production"

	event := ParseNginxLog(line, serviceName, environment)

	if event == nil {
		t.Fatal("ParseNginxLog returned nil for valid log")
	}

	// Check required fields
	if (*event)["service_name"] != serviceName {
		t.Errorf("Expected service_name '%s', got '%v'", serviceName, (*event)["service_name"])
	}

	if (*event)["event_type"] != "span" {
		t.Errorf("Expected event_type 'span', got '%v'", (*event)["event_type"])
	}

	if (*event)["operation"] != "GET /api/users" {
		t.Errorf("Expected operation 'GET /api/users', got '%v'", (*event)["operation"])
	}

	if (*event)["status_code"] != 200 {
		t.Errorf("Expected status_code 200, got '%v'", (*event)["status_code"])
	}

	// Check tags
	tags, ok := (*event)["tags"].(map[string]string)
	if !ok {
		t.Error("Expected tags to be map[string]string")
	} else {
		if tags["method"] != "GET" {
			t.Errorf("Expected method tag 'GET', got '%s'", tags["method"])
		}
		if tags["path"] != "/api/users" {
			t.Errorf("Expected path tag '/api/users', got '%s'", tags["path"])
		}
		if tags["client_ip"] != "192.168.1.1" {
			t.Errorf("Expected client_ip tag '192.168.1.1', got '%s'", tags["client_ip"])
		}
	}
}

func TestParseNginxLogInvalid(t *testing.T) {
	line := "This is not a valid Nginx log line"
	serviceName := "nginx-service"
	environment := "production"

	event := ParseNginxLog(line, serviceName, environment)

	if event != nil {
		t.Error("ParseNginxLog should return nil for invalid log")
	}
}

func TestParseNginxLogDifferentMethods(t *testing.T) {
	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			line := `192.168.1.1 - - [26/Oct/2024:10:30:15 +0000] "` + method + ` /api/test HTTP/1.1" 200 100`
			event := ParseNginxLog(line, "test", "production")

			if event == nil {
				t.Fatal("ParseNginxLog returned nil")
			}

			tags := (*event)["tags"].(map[string]string)
			if tags["method"] != method {
				t.Errorf("Expected method '%s', got '%s'", method, tags["method"])
			}

			expectedOp := method + " /api/test"
			if (*event)["operation"] != expectedOp {
				t.Errorf("Expected operation '%s', got '%v'", expectedOp, (*event)["operation"])
			}
		})
	}
}

func TestMapLogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"debug", "debug"},
		{"DEBUG", "debug"},
		{"info", "info"},
		{"INFO", "info"},
		{"warning", "warning"},
		{"WARNING", "warning"},
		{"error", "error"},
		{"ERROR", "error"},
		{"critical", "critical"},
		{"CRITICAL", "critical"},
		{"unknown", "info"}, // Unknown levels default to info
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := mapLogLevel(tt.input)
			if result != tt.expected {
				t.Errorf("mapLogLevel(%s) = %s, expected %s", tt.input, result, tt.expected)
			}
		})
	}
}
