package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/yaat/sidecar/internal/buffer"
	"github.com/yaat/sidecar/internal/config"
	"github.com/yaat/sidecar/internal/daemon"
	"github.com/yaat/sidecar/internal/forwarder"
	"github.com/yaat/sidecar/internal/health"
	"github.com/yaat/sidecar/internal/logs"
	"github.com/yaat/sidecar/internal/proxy"
	"github.com/yaat/sidecar/internal/selfupdate"
	"github.com/yaat/sidecar/internal/setup"
)

const version = "1.1.0"

func main() {
	var (
		configPath     = flag.String("config", "yaat.yaml", "Path to configuration file")
		showVersion    = flag.Bool("version", false, "Show version and exit")
		daemonMode     = flag.Bool("daemon", false, "Run in background (daemon mode)")
		daemonShort    = flag.Bool("d", false, "Run in background (short flag)")
		logFile        = flag.String("log-file", "", "Write logs to file instead of stderr")
		verbose        = flag.Bool("verbose", false, "Enable verbose/debug logging")
		verboseShort   = flag.Bool("v", false, "Enable verbose/debug logging (short flag)")
		initConfig     = flag.Bool("init", false, "Create sample configuration file")
		validateCfg    = flag.Bool("validate", false, "Validate configuration and exit")
		testAPIFlag    = flag.Bool("test", false, "Test API connection and exit")
		uninstall      = flag.Bool("uninstall", false, "Uninstall sidecar and cleanup")
		uninstallAlias = flag.Bool("uninsatll", false, "Uninstall sidecar (alias)")
		setupWizard    = flag.Bool("setup", false, "Launch interactive setup wizard")
		updateBinary   = flag.Bool("update", false, "Update sidecar to the latest release")
		startService   = flag.Bool("start", false, "Start sidecar as background service")
		stopService    = flag.Bool("stop", false, "Stop background sidecar service")
		restartService = flag.Bool("restart", false, "Restart background sidecar service")
		statusService  = flag.Bool("status", false, "Show background service status")
		healthPort     = flag.Int("health-port", 0, "Enable health check endpoint on this port")
	)
	flag.Parse()

	isVerbose := *verbose || *verboseShort
	isDaemon := *daemonMode || *daemonShort || *startService

	// Handle version flag
	if *showVersion {
		fmt.Printf("YAAT Sidecar v%s\n", version)
		fmt.Printf("Platform: %s/%s\n", getOS(), getArch())
		os.Exit(0)
	}

	// Handle update flag
	if *updateBinary {
		fmt.Println("Checking for updates...")
		result, err := selfupdate.Run(version)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
			os.Exit(1)
		}
		if result.Updated {
			fmt.Printf("✓ Updated YAAT Sidecar from %s to %s\n", result.FromVersion, result.ToVersion)
		} else {
			fmt.Printf("✓ Already running the latest version (%s)\n", result.ToVersion)
		}
		os.Exit(0)
	}

	// Handle setup wizard
	if *setupWizard {
		target := preferredConfigPath(*configPath)
		if err := setup.Run(target); err != nil {
			fmt.Fprintf(os.Stderr, "Setup failed: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Handle init flag - create sample config
	if *initConfig {
		target := preferredConfigPath(*configPath)
		if err := config.CreateSampleConfig(target); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Created sample configuration at %s\n", target)
		fmt.Println("Edit this file with your API key and settings, then run:")
		fmt.Println("  yaat-sidecar --config", target)
		os.Exit(0)
	}

	// Handle uninstall flag
	if *uninstall || *uninstallAlias {
		if err := daemon.Uninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "Uninstall failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ YAAT Sidecar uninstalled successfully")
		os.Exit(0)
	}

	// Handle stop flag
	if *stopService {
		if err := daemon.Stop(); err != nil {
			if isNotRunningError(err) {
				fmt.Println("ℹ️ Sidecar is not running")
				os.Exit(0)
			}
			fmt.Fprintf(os.Stderr, "Failed to stop sidecar: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ Sidecar stopped")
		os.Exit(0)
	}

	// Handle status flag
	if *statusService {
		if daemon.IsRunning() {
			pid := "unknown"
			if data, err := os.ReadFile(daemon.GetPidPath()); err == nil {
				if trimmed := strings.TrimSpace(string(data)); trimmed != "" {
					pid = trimmed
				}
			}
			fmt.Printf("✓ YAAT Sidecar is running (PID %s)\n", pid)
			fmt.Printf("  Logs: %s\n", daemon.GetLogPath())
		} else {
			fmt.Println("✗ YAAT Sidecar is not running")
		}
		os.Exit(0)
	}

	// Handle restart flag
	if *restartService {
		cfg, err := config.LoadConfig(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
			os.Exit(1)
		}
		if daemon.IsRunning() {
			if err := daemon.Stop(); err != nil && !isNotRunningError(err) {
				fmt.Fprintf(os.Stderr, "Failed to stop running sidecar: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("✓ Stopped existing sidecar")
		}
		if err := daemon.Start(cfg.SourcePath, *logFile, isVerbose); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to start sidecar: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ Sidecar restarted in background")
		fmt.Printf("  Logs: %s\n", daemon.GetLogPath())
		os.Exit(0)
	}

	// Setup logging
	setupLogging(*logFile, isVerbose)

	// Recover from any panics
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[PANIC] Sidecar crashed: %v", r)
			os.Exit(1)
		}
	}()

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("[Sidecar] Failed to load config: %v\nRun `yaat-sidecar --setup` to generate one.", err)
	}
	resolvedConfigPath := cfg.SourcePath

	// Handle validate flag
	if *validateCfg {
		fmt.Println("✓ Configuration is valid")
		fmt.Printf("  Service: %s\n", cfg.ServiceName)
		fmt.Printf("  Environment: %s\n", cfg.Environment)
		fmt.Printf("  API Endpoint: %s\n", cfg.APIEndpoint)
		fmt.Printf("  Proxy: %v\n", cfg.Proxy.Enabled)
		fmt.Printf("  Log files: %d\n", len(cfg.Logs))
		os.Exit(0)
	}

	// Handle test flag - test API connection
	if *testAPIFlag {
		fmt.Println("Testing API connection...")
		fwd := forwarder.New(cfg.APIEndpoint, cfg.APIKey)
		if err := fwd.Test(); err != nil {
			fmt.Fprintf(os.Stderr, "✗ API connection failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ API connection successful")
		os.Exit(0)
	}

	// Handle daemon mode
	if isDaemon {
		if err := daemon.Start(resolvedConfigPath, *logFile, isVerbose); err != nil {
			log.Fatalf("[Sidecar] Failed to start daemon: %v", err)
		}
		fmt.Println("✓ Sidecar started in background")
		fmt.Println("  Check logs with: tail -f", daemon.GetLogPath())
		fmt.Println("  Manage with: yaat-sidecar --status | --stop | --restart")
		os.Exit(0)
	}

	log.Printf("[Sidecar] YAAT Sidecar v%s starting...", version)
	log.Printf("[Sidecar] Config file: %s", resolvedConfigPath)

	log.Printf("[Sidecar] Service: %s (environment: %s)", cfg.ServiceName, cfg.Environment)
	log.Printf("[Sidecar] API endpoint: %s", cfg.APIEndpoint)
	log.Printf("[Sidecar] Buffer size: %d events", cfg.BufferSize)
	log.Printf("[Sidecar] Flush interval: %v", cfg.FlushIntervalDuration)

	// Create event buffer
	buf := buffer.New(cfg.BufferSize)

	// Create forwarder
	fwd := forwarder.New(cfg.APIEndpoint, cfg.APIKey)

	// Start periodic flusher
	stopFlusher := make(chan struct{})
	go periodicFlusher(buf, fwd, cfg.FlushIntervalDuration, stopFlusher)

	// Start log tailers
	if len(cfg.Logs) > 0 {
		log.Printf("[Sidecar] Starting %d log tailers...", len(cfg.Logs))
		for _, logCfg := range cfg.Logs {
			tailer := logs.New(logCfg.Path, logCfg.Format, cfg.ServiceName, cfg.Environment, buf)
			if err := tailer.Start(); err != nil {
				log.Printf("[Sidecar] Failed to start tailer for %s: %v", logCfg.Path, err)
			} else {
				log.Printf("[Sidecar] Tailing %s (format: %s)", logCfg.Path, logCfg.Format)
			}
		}
	}

	// Start HTTP proxy if enabled
	if cfg.Proxy.Enabled {
		log.Printf("[Sidecar] Starting HTTP proxy on port %d -> %s",
			cfg.Proxy.ListenPort, cfg.Proxy.UpstreamURL)

		proxy, err := proxy.New(
			cfg.Proxy.ListenPort,
			cfg.Proxy.UpstreamURL,
			cfg.ServiceName,
			cfg.Environment,
			buf,
		)
		if err != nil {
			log.Fatalf("[Sidecar] Failed to create proxy: %v", err)
		}

		go func() {
			if err := proxy.Start(); err != nil {
				log.Fatalf("[Sidecar] Proxy error: %v", err)
			}
		}()
	}

	// Start health check endpoint if configured
	if *healthPort > 0 {
		healthSvc := health.New(*healthPort, version, cfg.ServiceName)
		go func() {
			log.Printf("[Sidecar] Health endpoint running on :%d", *healthPort)
			if err := healthSvc.Start(); err != nil {
				log.Printf("[Sidecar] Health endpoint error: %v", err)
			}
		}()
	}

	log.Printf("[Sidecar] ✓ Sidecar running. Press Ctrl+C to stop.")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Printf("[Sidecar] Shutting down gracefully...")

	// Stop flusher
	close(stopFlusher)

	// Flush remaining events
	events := buf.Flush()
	if len(events) > 0 {
		log.Printf("[Sidecar] Flushing %d remaining events...", len(events))
		if err := fwd.Send(events); err != nil {
			log.Printf("[Sidecar] Failed to flush events: %v", err)
		}
	}

	log.Printf("[Sidecar] Shutdown complete.")
}

// periodicFlusher flushes the buffer periodically
func periodicFlusher(buf *buffer.Buffer, fwd *forwarder.Forwarder, interval time.Duration, stop chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Flush buffer
			events := buf.Flush()
			if len(events) == 0 {
				continue
			}

			log.Printf("[Flusher] Flushing %d events...", len(events))
			if err := fwd.Send(events); err != nil {
				log.Printf("[Flusher] Failed to send events: %v", err)
				// Events are lost - could implement persistent queue here
			}

		case <-stop:
			log.Printf("[Flusher] Stopped")
			return
		}
	}
}

// setupLogging configures logging based on flags
func setupLogging(logFilePath string, verbose bool) {
	// Set log format
	if verbose {
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	} else {
		log.SetFlags(log.Ldate | log.Ltime)
	}

	// Setup output destination
	if logFilePath != "" {
		f, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to open log file: %v\n", err)
			os.Exit(1)
		}
		log.SetOutput(f)
	} else {
		log.SetOutput(os.Stderr)
	}
}

// getOS returns the current operating system
func getOS() string {
	switch {
	case os.Getenv("OS") == "Windows_NT":
		return "windows"
	default:
		// Check for Darwin or Linux
		if _, err := os.Stat("/System/Library/CoreServices/SystemVersion.plist"); err == nil {
			return "darwin"
		}
		return "linux"
	}
}

// getArch returns the current architecture
func getArch() string {
	// This is a simplified version - in production you'd check runtime.GOARCH
	return "amd64" // Placeholder
}

func preferredConfigPath(provided string) string {
	if provided != "" && provided != "yaat.yaml" {
		return provided
	}
	return config.DefaultConfigPath()
}

func isNotRunningError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "not running")
}
