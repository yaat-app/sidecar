package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yaat/sidecar/internal/buffer"
	"github.com/yaat/sidecar/internal/config"
	"github.com/yaat/sidecar/internal/daemon"
	"github.com/yaat/sidecar/internal/forwarder"
	"github.com/yaat/sidecar/internal/health"
	"github.com/yaat/sidecar/internal/logs"
	"github.com/yaat/sidecar/internal/proxy"
)

const version = "1.0.0"

func main() {
	// Parse command-line flags
	var (
		configPath    = flag.String("config", "yaat.yaml", "Path to configuration file")
		showVersion   = flag.Bool("version", false, "Show version and exit")
		daemonMode    = flag.Bool("daemon", false, "Run in background (daemon mode)")
		daemonShort   = flag.Bool("d", false, "Run in background (short flag)")
		logFile       = flag.String("log-file", "", "Write logs to file instead of stderr")
		verbose       = flag.Bool("verbose", false, "Enable verbose/debug logging")
		verboseShort  = flag.Bool("v", false, "Enable verbose/debug logging (short flag)")
		initConfig    = flag.Bool("init", false, "Create sample configuration file")
		validateCfg   = flag.Bool("validate", false, "Validate configuration and exit")
		testAPI       = flag.Bool("test", false, "Test API connection and exit")
		uninstall     = flag.Bool("uninstall", false, "Uninstall sidecar and cleanup")
		healthPort    = flag.Int("health-port", 0, "Enable health check endpoint on this port")
	)
	flag.Parse()

	// Handle version flag
	if *showVersion {
		fmt.Printf("YAAT Sidecar v%s\n", version)
		fmt.Printf("Platform: %s/%s\n", getOS(), getArch())
		os.Exit(0)
	}

	// Handle init flag - create sample config
	if *initConfig {
		if err := config.CreateSampleConfig(*configPath); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Created sample configuration at %s\n", *configPath)
		fmt.Println("Edit this file with your API key and settings, then run:")
		fmt.Println("  yaat-sidecar --config", *configPath)
		os.Exit(0)
	}

	// Handle uninstall flag
	if *uninstall {
		if err := daemon.Uninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "Uninstall failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ YAAT Sidecar uninstalled successfully")
		os.Exit(0)
	}

	// Combine short and long flags
	isDaemon := *daemonMode || *daemonShort
	isVerbose := *verbose || *verboseShort

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
		log.Fatalf("[Sidecar] Failed to load config: %v", err)
	}

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
	if *testAPI {
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
		if err := daemon.Start(*configPath, *logFile, isVerbose); err != nil {
			log.Fatalf("[Sidecar] Failed to start daemon: %v", err)
		}
		fmt.Println("✓ Sidecar started in background")
		fmt.Println("  Check logs with: tail -f", daemon.GetLogPath())
		fmt.Println("  Stop with: kill $(cat", daemon.GetPidPath()+")")
		os.Exit(0)
	}

	log.Printf("[Sidecar] YAAT Sidecar v%s starting...", version)
	log.Printf("[Sidecar] Config file: %s", *configPath)

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
