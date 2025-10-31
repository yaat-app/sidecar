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
	OrganizationID string          `yaml:"organization_id"`
	APIKey         string          `yaml:"api_key"`
	ServiceName    string          `yaml:"service_name"`
	Environment    string          `yaml:"environment"`
	Tags           map[string]string `yaml:"tags,omitempty"`     // Global tags for all events
	Proxy         ProxyConfig     `yaml:"proxy"`
	Logs          []LogConfig     `yaml:"logs"`
	BufferSize    int             `yaml:"buffer_size"`
	FlushInterval string          `yaml:"flush_interval"`
	APIEndpoint   string          `yaml:"api_endpoint"`
	Delivery      DeliveryConfig  `yaml:"delivery"`
	Metrics       MetricsConfig   `yaml:"metrics"`
	Scrubbing     ScrubbingConfig `yaml:"scrubbing"`
	Analytics     AnalyticsConfig `yaml:"analytics"`

	// Parsed flush interval
	FlushIntervalDuration time.Duration `yaml:"-"`
	SourcePath            string        `yaml:"-"`
}

// DeliveryConfig tunes forwarding behaviour.
type DeliveryConfig struct {
	BatchSize                   int           `yaml:"batch_size"`            // max events per HTTP request
	Compress                    bool          `yaml:"compress"`              // gzip payloads
	MaxBatchBytes               int           `yaml:"max_batch_bytes"`       // optional soft limit (0 disables)
	QueueRetention              string        `yaml:"queue_retention"`       // e.g. "24h", "0s" disables
	DeadLetterRetention         string        `yaml:"dead_letter_retention"` // e.g. "168h"
	QueueRetentionDuration      time.Duration `yaml:"-"`
	DeadLetterRetentionDuration time.Duration `yaml:"-"`
}

// MetricsConfig controls host metrics collection.
type MetricsConfig struct {
	Enabled          bool              `yaml:"enabled"`
	Interval         string            `yaml:"interval"`
	Tags             map[string]string `yaml:"tags,omitempty"`
	IntervalDuration time.Duration     `yaml:"-"`
	StatsD           StatsDConfig      `yaml:"statsd"`
}

// StatsDConfig controls the embedded StatsD/dogstatsd listener.
type StatsDConfig struct {
	Enabled    bool              `yaml:"enabled"`
	ListenAddr string            `yaml:"listen_addr"`
	Namespace  string            `yaml:"namespace"`
	Tags       map[string]string `yaml:"tags,omitempty"`
}

// ScrubbingConfig controls regex-based redaction/drop rules.
type ScrubbingConfig struct {
	Enabled bool        `yaml:"enabled"`
	Rules   []ScrubRule `yaml:"rules"`
}

// ScrubRule describes an individual regex replacement/drop instruction.
type ScrubRule struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Pattern     string   `yaml:"pattern"`
	Replacement string   `yaml:"replacement,omitempty"`
	Fields      []string `yaml:"fields,omitempty"`
	Drop        bool     `yaml:"drop,omitempty"`
}

// AnalyticsConfig controls local DuckDB analytics storage.
type AnalyticsConfig struct {
	Enabled          bool              `yaml:"enabled"`
	DatabasePath     string            `yaml:"database_path"`
	RetentionDays    int               `yaml:"retention_days"`
	MaxSizeGB        float64           `yaml:"max_size_gb"`
	BatchSize        int               `yaml:"batch_size"`
	WriteTimeout     string            `yaml:"write_timeout"`
	TimeoutDuration  time.Duration     `yaml:"-"`
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

# Your YAAT organization ID (required)
# Get this from: https://yaat.io → Settings → Organization
organization_id: "org_your_organization_id_here"

# Your YAAT organization API key (required)
# Get this from: https://yaat.io → Settings → API Keys
api_key: "yaat_your_api_key_here"

# Service name (required)
# This identifies your service in the YAAT dashboard
service_name: "my-service"

# Environment (optional, default: production)
# Examples: production, staging, development
environment: "production"

# Global Tags (optional)
# Tags applied to all events (logs, spans, metrics)
# Cloud provider (AWS/GCP/Azure) and Kubernetes metadata are auto-detected
# and merged with custom tags. Custom tags take priority.
# tags:
#   team: "backend"
#   version: "v1.2.3"
#   region: "us-west-2"

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

# Delivery tuning
delivery:
  batch_size: 500           # Max events per HTTP request
  compress: true            # Gzip compress payloads
  max_batch_bytes: 0        # Optional soft limit in bytes (0 to disable)
  queue_retention: "24h"    # How long to keep persisted batches before cleanup
  dead_letter_retention: "168h" # Retention for dead-letter batches

# Host metrics
metrics:
  enabled: false            # Set to true to publish host metrics
  interval: "30s"           # Sampling interval
  tags: {}                  # Optional static tags applied to host metrics
  statsd:
    enabled: false          # Enable embedded StatsD/dogstatsd listener
    listen_addr: ":8125"   # UDP address to listen on (host:port or :port)
    namespace: ""          # Optional prefix added to metric names
    tags: {}                # Additional tags applied to all StatsD metrics

# Data scrubbing (mask sensitive values before sending to YAAT)
scrubbing:
  enabled: true
  rules:
    - name: "Mask Authorization bearer tokens"
      description: "Replaces Bearer tokens in log messages"
      pattern: "(?i)(authorization:?\\s*bearer\\s+)[A-Za-z0-9._~-]+"
      replacement: "$1[REDACTED]"
      fields: ["message", "stacktrace", "tags.authorization"]
    - name: "Mask email addresses"
      description: "Removes email-like patterns from payloads"
      pattern: "(?i)[A-Z0-9._%+-]+@[A-Z0-9.-]+\\.[A-Z]{2,}"
      replacement: "[EMAIL]"
      fields: ["message", "stacktrace", "tags.*"]

# Local Analytics (DuckDB)
# Store events locally for instant SQL queries
# Works offline (no API key needed) or alongside cloud sync
analytics:
  enabled: true                 # Enable local analytics database
  database_path: "~/.yaat/analytics.db"  # Local database file
  retention_days: 14            # Keep events for 14 days
  max_size_gb: 2.0              # Max database size (auto-cleanup when exceeded)
  batch_size: 500               # Events per transaction
  write_timeout: "5s"           # Per-batch write timeout

# YAAT API endpoint (required for cloud mode)
# Production: https://yaat.io/api/v1/ingest
# Staging: https://staging.yaat.io/api/v1/ingest
# Leave blank or omit api_key for local-only mode
api_endpoint: "https://yaat.io/api/v1/ingest"
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
	if cfg.ServiceName == "" {
		return fmt.Errorf("service_name is required")
	}

	// API key and organization ID are not required in local-only mode
	if cfg.APIKey != "" {
		if cfg.OrganizationID == "" {
			return fmt.Errorf("organization_id is required when api_key is set")
		}
		if cfg.APIEndpoint == "" {
			return fmt.Errorf("api_endpoint is required when api_key is set")
		}
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
	if cfg.Delivery.BatchSize <= 0 {
		cfg.Delivery.BatchSize = 500
	}
	if cfg.Delivery.MaxBatchBytes < 0 {
		cfg.Delivery.MaxBatchBytes = 0
	}
	if cfg.Delivery.QueueRetention == "" {
		cfg.Delivery.QueueRetention = "24h"
	}
	if cfg.Delivery.QueueRetention != "" {
		if dur, err := time.ParseDuration(cfg.Delivery.QueueRetention); err == nil {
			cfg.Delivery.QueueRetentionDuration = dur
		} else {
			return fmt.Errorf("invalid delivery.queue_retention: %w", err)
		}
	}
	if cfg.Delivery.DeadLetterRetention == "" {
		cfg.Delivery.DeadLetterRetention = "168h"
	}
	if cfg.Delivery.DeadLetterRetention != "" {
		if dur, err := time.ParseDuration(cfg.Delivery.DeadLetterRetention); err == nil {
			cfg.Delivery.DeadLetterRetentionDuration = dur
		} else {
			return fmt.Errorf("invalid delivery.dead_letter_retention: %w", err)
		}
	}
	if cfg.Metrics.Enabled {
		if cfg.Metrics.Interval == "" {
			cfg.Metrics.Interval = "30s"
		}
		if cfg.Metrics.StatsD.ListenAddr == "" {
			cfg.Metrics.StatsD.ListenAddr = ":8125"
		}
	}
	if cfg.Metrics.Interval != "" {
		dur, err := time.ParseDuration(cfg.Metrics.Interval)
		if err != nil {
			return fmt.Errorf("invalid metrics.interval: %w", err)
		}
		cfg.Metrics.IntervalDuration = dur
	}
	for i := range cfg.Scrubbing.Rules {
		if cfg.Scrubbing.Rules[i].Replacement == "" && !cfg.Scrubbing.Rules[i].Drop {
			cfg.Scrubbing.Rules[i].Replacement = "[REDACTED]"
		}
	}
	duration, err := time.ParseDuration(cfg.FlushInterval)
	if err != nil {
		return fmt.Errorf("invalid flush_interval: %w", err)
	}
	cfg.FlushIntervalDuration = duration

	// Analytics defaults
	if cfg.Analytics.DatabasePath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cfg.Analytics.DatabasePath = filepath.Join(home, ".yaat", "analytics.db")
		} else {
			cfg.Analytics.DatabasePath = ".yaat/analytics.db"
		}
	}
	if cfg.Analytics.RetentionDays == 0 {
		cfg.Analytics.RetentionDays = 14
	}
	if cfg.Analytics.MaxSizeGB == 0 {
		cfg.Analytics.MaxSizeGB = 2.0
	}
	if cfg.Analytics.BatchSize == 0 {
		cfg.Analytics.BatchSize = 500
	}
	if cfg.Analytics.WriteTimeout == "" {
		cfg.Analytics.WriteTimeout = "5s"
	}
	if cfg.Analytics.WriteTimeout != "" {
		dur, err := time.ParseDuration(cfg.Analytics.WriteTimeout)
		if err != nil {
			return fmt.Errorf("invalid analytics.write_timeout: %w", err)
		}
		cfg.Analytics.TimeoutDuration = dur
	}

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

// RecommendedScrubRules returns a curated set of baseline redaction rules.
func RecommendedScrubRules() []ScrubRule {
	return []ScrubRule{
		{
			Name:        "Mask Authorization bearer tokens",
			Description: "Replaces Bearer tokens in log messages",
			Pattern:     `(?i)(authorization:?\s*bearer\s+)[A-Za-z0-9._~-]+`,
			Replacement: `$1[REDACTED]`,
			Fields:      []string{"message", "stacktrace", "tags.authorization"},
		},
		{
			Name:        "Mask email addresses",
			Description: "Removes email-like patterns from payloads",
			Pattern:     `(?i)[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}`,
			Replacement: `[EMAIL]`,
			Fields:      []string{"message", "stacktrace", "tags.*"},
		},
		{
			Name:        "Mask UUID-like identifiers",
			Description: "Scrubs UUID/GUID values often used as user identifiers",
			Pattern:     `(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`,
			Replacement: `[UUID]`,
			Fields:      []string{"message", "stacktrace", "tags.*"},
		},
	}
}
