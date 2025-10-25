package setup

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yaat/sidecar/internal/config"
	"github.com/yaat/sidecar/internal/daemon"
	"github.com/yaat/sidecar/internal/forwarder"
)

// Run executes the interactive setup wizard.
func Run(configPath string) error {
	fmt.Println("╭──────────────────────────────────────────────╮")
	fmt.Println("│ YAAT Sidecar interactive setup               │")
	fmt.Println("╰──────────────────────────────────────────────╯")
	fmt.Println("This wizard will create an optimised configuration and")
	fmt.Println("optionally start the sidecar as a background service.")
	fmt.Println()

	if configPath == "" {
		configPath = config.DefaultConfigPath()
	}

	reader := bufio.NewReader(os.Stdin)
	cfg := bootstrapConfig(configPath)

	cfg.APIKey = promptAPIKey(reader, cfg.APIKey)
	cfg.ServiceName = promptString(reader, "Service name", cfg.ServiceName)
	cfg.Environment = promptString(reader, "Environment", cfg.Environment)

	fmt.Println()
	fmt.Println("Proxy settings (capture HTTP traffic via reverse proxy):")
	enableProxy := promptYesNo(reader, "Enable built-in proxy?", cfg.Proxy.Enabled)
	cfg.Proxy.Enabled = enableProxy
	if enableProxy {
		cfg.Proxy.ListenPort = promptInt(reader, "Proxy listen port", defaultPort(cfg.Proxy.ListenPort))
		cfg.Proxy.UpstreamURL = promptString(reader, "Upstream URL", defaultUpstream(cfg.Proxy.UpstreamURL))
	} else {
		cfg.Proxy.ListenPort = 0
		cfg.Proxy.UpstreamURL = ""
	}

	fmt.Println()
	fmt.Println("Log tailing (capture structured events from existing log files):")
	selectedLogs := dedupeLogs(cfg.Logs)
	candidates := discoverLogCandidates()
	for _, cand := range candidates {
		if containsLog(selectedLogs, cand.Path) {
			continue
		}
		if promptYesNo(reader, fmt.Sprintf("Monitor %s (%s)?", cand.Path, cand.Description), false) {
			selectedLogs = append(selectedLogs, config.LogConfig{
				Path:   cand.Path,
				Format: cand.Format,
			})
		}
	}
	for {
		path := promptString(reader, "Add another log file (leave empty to continue)", "")
		if path == "" {
			break
		}
		format := promptLogFormat(reader)
		absPath := expandPath(path)
		if containsLog(selectedLogs, absPath) {
			fmt.Println("  Already added, skipping.")
			continue
		}
		selectedLogs = append(selectedLogs, config.LogConfig{
			Path:   absPath,
			Format: format,
		})
	}
	cfg.Logs = selectedLogs

	fmt.Println()
	cfg.BufferSize = promptInt(reader, "Buffer size", cfg.BufferSize)
	cfg.FlushInterval = promptDuration(reader, "Flush interval", cfg.FlushInterval)
	cfg.APIEndpoint = promptString(reader, "YAAT ingest endpoint", cfg.APIEndpoint)

	fmt.Println()
	if err := config.SaveConfig(configPath, cfg); err != nil {
		return err
	}

	fmt.Printf("✓ Configuration saved to %s\n", cfg.SourcePath)

	if promptYesNo(reader, "Test API connectivity now?", false) {
		if err := testAPI(cfg); err != nil {
			fmt.Printf("✗ API test failed: %v\n", err)
		} else {
			fmt.Println("✓ API connection successful")
		}
	}

	if promptYesNo(reader, "Start YAAT Sidecar in the background?", true) {
		if err := daemon.Start(cfg.SourcePath, "", false); err != nil {
			fmt.Printf("✗ Failed to start daemon: %v\n", err)
		} else {
			fmt.Println("✓ Sidecar started successfully")
			fmt.Printf("  Logs: %s\n", daemon.GetLogPath())
		}
	}

	fmt.Println()
	fmt.Println("Setup complete. You can re-run this wizard any time with `yaat-sidecar --setup`.")
	return nil
}

type logCandidate struct {
	Path        string
	Format      string
	Description string
}

func bootstrapConfig(path string) *config.Config {
	if existing, err := config.LoadConfig(path); err == nil {
		fmt.Printf("Found existing configuration at %s. Press enter to keep current values.\n", existing.SourcePath)
		return existing
	}

	cfg := &config.Config{
		APIEndpoint: "https://yaat.io/v1/ingest",
		Environment: detectEnvironment(),
		BufferSize:  1000,
		FlushInterval: func() string {
			if val := os.Getenv("YAAT_FLUSH_INTERVAL"); val != "" {
				return val
			}
			return "10s"
		}(),
		ServiceName: detectServiceName(),
		Proxy: config.ProxyConfig{
			Enabled:     false,
			ListenPort:  19000,
			UpstreamURL: "http://127.0.0.1:8000",
		},
		Logs: []config.LogConfig{},
	}
	if apiKey := os.Getenv("YAAT_API_KEY"); apiKey != "" {
		cfg.APIKey = apiKey
	}
	if endpoint := os.Getenv("YAAT_API_ENDPOINT"); endpoint != "" {
		cfg.APIEndpoint = endpoint
	}
	if svc := os.Getenv("YAAT_SERVICE_NAME"); svc != "" {
		cfg.ServiceName = svc
	}
	return cfg
}

func detectEnvironment() string {
	if env := os.Getenv("YAAT_ENVIRONMENT"); env != "" {
		return env
	}
	if env := os.Getenv("ENVIRONMENT"); env != "" {
		return env
	}
	return "production"
}

func detectServiceName() string {
	if name := os.Getenv("YAAT_SERVICE_NAME"); name != "" {
		return name
	}
	if name := os.Getenv("SERVICE_NAME"); name != "" {
		return name
	}
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		return hostname
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Base(wd)
	}
	return "my-service"
}

func discoverLogCandidates() []logCandidate {
	candidates := []logCandidate{
		{Path: "/var/log/nginx/access.log", Format: "nginx", Description: "Nginx access log"},
		{Path: "/var/log/nginx/error.log", Format: "nginx", Description: "Nginx error log"},
		{Path: "/var/log/httpd/access_log", Format: "nginx", Description: "Apache access log"},
		{Path: "/var/log/syslog", Format: "json", Description: "System log"},
		{Path: "./logs/app.log", Format: "django", Description: "Local app log"},
		{Path: "./logs/server.log", Format: "json", Description: "Local server log"},
	}

	available := make([]logCandidate, 0, len(candidates))
	for _, cand := range candidates {
		if fileExists(cand.Path) {
			available = append(available, logCandidate{
				Path:        expandPath(cand.Path),
				Format:      cand.Format,
				Description: cand.Description,
			})
		}
	}
	return available
}

func promptAPIKey(reader *bufio.Reader, current string) string {
	for {
		if current != "" {
			fmt.Printf("YAAT API key [%s]: ", maskSecret(current))
		} else {
			fmt.Print("YAAT API key: ")
		}
		value := readLine(reader)
		if value == "" {
			if current != "" {
				return current
			}
			fmt.Println("  API key is required.")
			continue
		}
		return value
	}
}

func promptString(reader *bufio.Reader, label, defaultValue string) string {
	prompt := label
	if defaultValue != "" {
		prompt = fmt.Sprintf("%s [%s]", label, defaultValue)
	}
	fmt.Printf("%s: ", prompt)
	value := readLine(reader)
	if value == "" {
		return defaultValue
	}
	return value
}

func promptYesNo(reader *bufio.Reader, question string, defaultYes bool) bool {
	options := "y/N"
	if defaultYes {
		options = "Y/n"
	}
	for {
		fmt.Printf("%s (%s): ", question, options)
		answer := strings.ToLower(strings.TrimSpace(readLine(reader)))
		if answer == "" {
			return defaultYes
		}
		switch answer {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		default:
			fmt.Println("  Please answer with yes or no.")
		}
	}
}

func promptInt(reader *bufio.Reader, label string, defaultValue int) int {
	for {
		answer := promptString(reader, label, strconv.Itoa(defaultValue))
		value, err := strconv.Atoi(answer)
		if err != nil || value <= 0 {
			fmt.Println("  Enter a positive number.")
			continue
		}
		return value
	}
}

func promptDuration(reader *bufio.Reader, label, defaultValue string) string {
	for {
		answer := promptString(reader, label, defaultValue)
		if _, err := time.ParseDuration(answer); err != nil {
			fmt.Println("  Invalid duration. Examples: 10s, 1m, 30s")
			continue
		}
		return answer
	}
}

func promptLogFormat(reader *bufio.Reader) string {
	for {
		fmt.Print("Log format [django/nginx/json] (default: json): ")
		value := strings.ToLower(strings.TrimSpace(readLine(reader)))
		if value == "" {
			return "json"
		}
		switch value {
		case "django", "nginx", "json":
			return value
		default:
			fmt.Println("  Unsupported format. Choose django, nginx, or json.")
		}
	}
}

func testAPI(cfg *config.Config) error {
	forwarder := forwarder.New(cfg.APIEndpoint, cfg.APIKey)
	return forwarder.Test()
}

func fileExists(path string) bool {
	_, err := os.Stat(expandPath(path))
	return err == nil
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

func readLine(reader *bufio.Reader) string {
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}

func maskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 6 {
		return strings.Repeat("*", len(secret))
	}
	return secret[:4] + strings.Repeat("*", len(secret)-6) + secret[len(secret)-2:]
}

func defaultPort(current int) int {
	if current > 0 {
		return current
	}
	return 19000
}

func defaultUpstream(current string) string {
	if current != "" {
		return current
	}
	return "http://127.0.0.1:8000"
}

func containsLog(logs []config.LogConfig, path string) bool {
	for _, logCfg := range logs {
		if logCfg.Path == path {
			return true
		}
	}
	return false
}

func dedupeLogs(logs []config.LogConfig) []config.LogConfig {
	result := make([]config.LogConfig, 0, len(logs))
	seen := map[string]struct{}{}
	for _, logCfg := range logs {
		normalized := expandPath(logCfg.Path)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, config.LogConfig{
			Path:   normalized,
			Format: strings.ToLower(logCfg.Format),
		})
	}
	return result
}
