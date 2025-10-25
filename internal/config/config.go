package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	APIKey        string      `yaml:"api_key"`
	ServiceName   string      `yaml:"service_name"`
	Environment   string      `yaml:"environment"`
	Proxy         ProxyConfig `yaml:"proxy"`
	Logs          []LogConfig `yaml:"logs"`
	BufferSize    int         `yaml:"buffer_size"`
	FlushInterval string      `yaml:"flush_interval"`
	APIEndpoint   string      `yaml:"api_endpoint"`

	// Parsed flush interval
	FlushIntervalDuration time.Duration `yaml:"-"`
	SourcePath            string        `yaml:"-"`
}

// LoadConfig loads configuration from a YAML file
func LoadConfig(path string) (*Config, error) {
	data, resolvedPath, err := readConfig(path)
	if err != nil {
		return nil, err
	}

	// Parse YAML
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	cfg.SourcePath = resolvedPath

	if err := cfg.applyDefaults(); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}

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

	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create config directory %s: %w", dir, err)
		}
	}

	return os.WriteFile(path, []byte(sampleConfig), 0o600)
}

// SaveConfig persists the configuration to disk, creating parent directories when required.
func SaveConfig(path string, cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	if err := cfg.validate(); err != nil {
		return err
	}
	if err := cfg.applyDefaults(); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create config directory %s: %w", dir, err)
		}
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	cfg.SourcePath = path
	return nil
}

// DefaultConfigPath returns the recommended location for the config file.
func DefaultConfigPath() string {
	if override := os.Getenv("YAAT_CONFIG_PATH"); override != "" {
		return override
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".yaat", "yaat.yaml")
	}
	return "yaat.yaml"
}

func (cfg *Config) validate() error {
	if cfg.APIKey == "" {
		return fmt.Errorf("api_key is required")
	}
	if cfg.ServiceName == "" {
		return fmt.Errorf("service_name is required")
	}
	if cfg.APIEndpoint == "" {
		return fmt.Errorf("api_endpoint is required")
	}
	return nil
}

func (cfg *Config) applyDefaults() error {
	if cfg.Environment == "" {
		cfg.Environment = "production"
	}
	if cfg.BufferSize == 0 {
		cfg.BufferSize = 1000
	}
	if cfg.FlushInterval == "" {
		cfg.FlushInterval = "10s"
	}
	duration, err := time.ParseDuration(cfg.FlushInterval)
	if err != nil {
		return fmt.Errorf("invalid flush_interval: %w", err)
	}
	cfg.FlushIntervalDuration = duration
	return nil
}

func readConfig(path string) ([]byte, string, error) {
	candidates := []string{path}

	// When a relative filename is provided, probe common locations.
	if !filepath.IsAbs(path) {
		if wd, err := os.Getwd(); err == nil {
			candidates = append(candidates, filepath.Join(wd, path))
		}
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			candidates = append(candidates,
				filepath.Join(home, ".yaat", path),
				filepath.Join(home, ".config", "yaat", path),
			)
		}
		candidates = append(candidates, filepath.Join("/etc/yaat", path))
	}

	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		clean := filepath.Clean(candidate)
		if _, exists := seen[clean]; exists {
			continue
		}
		seen[clean] = struct{}{}

		data, err := os.ReadFile(clean)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, "", fmt.Errorf("failed to read config file %s: %w", clean, err)
		}
		return data, clean, nil
	}

	return nil, "", fmt.Errorf("config file not found (searched: %s)", strings.Join(uniquePaths(candidates), ", "))
}

func uniquePaths(paths []string) []string {
	unique := make([]string, 0, len(paths))
	seen := make(map[string]struct{})
	for _, p := range paths {
		clean := filepath.Clean(p)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		unique = append(unique, clean)
	}
	return unique
}
