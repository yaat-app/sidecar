package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// ProxyConfig holds HTTP proxy configuration
type ProxyConfig struct {
	Enabled     bool   `yaml:"enabled"`
	ListenPort  int    `yaml:"listen_port"`
	UpstreamURL string `yaml:"upstream_url"`
}

// LogConfig holds log file configuration
type LogConfig struct {
	Path   string `yaml:"path"`
	Format string `yaml:"format"` // "django", "nginx", "json"
}

// Config represents the sidecar configuration
type Config struct {
	APIKey        string        `yaml:"api_key"`
	ServiceName   string        `yaml:"service_name"`
	Environment   string        `yaml:"environment"`
	Proxy         ProxyConfig   `yaml:"proxy"`
	Logs          []LogConfig   `yaml:"logs"`
	BufferSize    int           `yaml:"buffer_size"`
	FlushInterval string        `yaml:"flush_interval"`
	APIEndpoint   string        `yaml:"api_endpoint"`

	// Parsed flush interval
	FlushIntervalDuration time.Duration `yaml:"-"`
}

// LoadConfig loads configuration from a YAML file
func LoadConfig(path string) (*Config, error) {
	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse YAML
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Validate required fields
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("api_key is required")
	}
	if cfg.ServiceName == "" {
		return nil, fmt.Errorf("service_name is required")
	}
	if cfg.APIEndpoint == "" {
		return nil, fmt.Errorf("api_endpoint is required")
	}

	// Set defaults
	if cfg.Environment == "" {
		cfg.Environment = "production"
	}
	if cfg.BufferSize == 0 {
		cfg.BufferSize = 1000
	}
	if cfg.FlushInterval == "" {
		cfg.FlushInterval = "10s"
	}

	// Parse flush interval
	duration, err := time.ParseDuration(cfg.FlushInterval)
	if err != nil {
		return nil, fmt.Errorf("invalid flush_interval: %w", err)
	}
	cfg.FlushIntervalDuration = duration

	return &cfg, nil
}

// CreateSampleConfig creates a sample configuration file
func CreateSampleConfig(path string) error {
	sampleConfig := `# YAAT Sidecar Configuration
# For more information, visit: https://docs.yaat.io

# Your YAAT organization API key (required)
# Get this from: https://yaat.io → Settings → API Keys
api_key: "yaat_your_api_key_here"

# Service name (required)
# This identifies your service in the YAAT dashboard
service_name: "my-service"

# Environment (optional, default: production)
# Examples: production, staging, development
environment: "production"

# HTTP Proxy Configuration (optional)
# Monitor HTTP traffic by proxying requests to your application
proxy:
  enabled: false
  listen_port: 19000          # Port for sidecar to listen on
  upstream_url: "http://127.0.0.1:8000"  # Your application's URL

# Log File Monitoring (optional)
# Monitor multiple log files with different formats
logs:
  # Example: Django application logs
  - path: "/var/log/myapp/app.log"
    format: "django"  # Options: django, nginx, json

  # Example: Nginx access logs
  # - path: "/var/log/nginx/access.log"
  #   format: "nginx"

  # Example: JSON logs
  # - path: "/var/log/myapp/events.json"
  #   format: "json"

# Event buffering configuration
buffer_size: 1000           # Number of events to buffer before flushing
flush_interval: "10s"       # How often to send events (e.g., 10s, 1m, 30s)

# YAAT API endpoint (required)
# Production: https://yaat.io/v1/ingest
# Staging: https://staging.yaat.io/v1/ingest
api_endpoint: "https://yaat.io/v1/ingest"
`

	return os.WriteFile(path, []byte(sampleConfig), 0644)
}
