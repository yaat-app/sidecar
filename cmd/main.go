package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yaat/sidecar/internal/buffer"
	"github.com/yaat/sidecar/internal/config"
	"github.com/yaat/sidecar/internal/forwarder"
	"github.com/yaat/sidecar/internal/logs"
	"github.com/yaat/sidecar/internal/proxy"
)

func main() {
	// Parse command-line flags
	configPath := flag.String("config", "yaat.yaml", "Path to configuration file")
	flag.Parse()

	// Setup logging
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.SetOutput(os.Stderr) // Never write to customer's logs

	// Recover from any panics
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[PANIC] Sidecar crashed: %v", r)
			os.Exit(1)
		}
	}()

	log.Printf("[Sidecar] YAAT Sidecar starting...")
	log.Printf("[Sidecar] Config file: %s", *configPath)

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("[Sidecar] Failed to load config: %v", err)
	}

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
