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

	"github.com/yaat-app/sidecar/internal/analytics"
	"github.com/yaat-app/sidecar/internal/buffer"
	"github.com/yaat-app/sidecar/internal/config"
	"github.com/yaat-app/sidecar/internal/daemon"
	"github.com/yaat-app/sidecar/internal/detection"
	"github.com/yaat-app/sidecar/internal/diag"
	"github.com/yaat-app/sidecar/internal/forwarder"
	"github.com/yaat-app/sidecar/internal/health"
	"github.com/yaat-app/sidecar/internal/logs"
	"github.com/yaat-app/sidecar/internal/metrics"
	"github.com/yaat-app/sidecar/internal/proxy"
	"github.com/yaat-app/sidecar/internal/queue"
	"github.com/yaat-app/sidecar/internal/scrubber"
	"github.com/yaat-app/sidecar/internal/selfupdate"
	"github.com/yaat-app/sidecar/internal/setup"
	"github.com/yaat-app/sidecar/internal/state"
	"github.com/yaat-app/sidecar/internal/statsd"
	"github.com/yaat-app/sidecar/internal/tui"
)

const version = "0.0.11-alpha"

func main() {
	var (
		configPath     = flag.String("config", "yaat.yaml", "Path to configuration file")
		instanceName   = flag.String("instance", "default", "Instance name for multi-instance deployments")
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
		dashboardUI    = flag.Bool("dashboard", false, "Launch interactive dashboard (TUI)")
		uiAlias        = flag.Bool("ui", false, "Launch interactive dashboard (alias)")
	)
	flag.Parse()

	isVerbose := *verbose || *verboseShort
	isDaemon := *daemonMode || *daemonShort || *startService

	// Check if no flags were provided - if so, launch dashboard
	noFlagsProvided := flag.NFlag() == 0 && !isDaemon

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

	// Handle dashboard UI (or default to it if no flags)
	if *dashboardUI || *uiAlias || noFlagsProvided {
		if err := tui.RunDashboard(); err != nil {
			fmt.Fprintf(os.Stderr, "Dashboard failed: %v\n", err)
			os.Exit(1)
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
		warnings, err := daemon.Uninstall()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Uninstall failed: %v\n", err)
			os.Exit(1)
		}
		if len(warnings) > 0 {
			fmt.Println("✓ YAAT Sidecar uninstalled with warnings")
		} else {
			fmt.Println("✓ YAAT Sidecar uninstalled successfully")
		}
		os.Exit(0)
	}

	// Handle stop flag
	if *stopService {
		pidPath := getInstancePIDPath(*instanceName)
		if err := daemon.Stop(pidPath); err != nil {
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
		pidPath := getInstancePIDPath(*instanceName)
		logPath := getInstanceLogPath(*instanceName)
		if daemon.IsRunning(pidPath) {
			pid := "unknown"
			if data, err := os.ReadFile(daemon.GetPidPath(pidPath)); err == nil {
				if trimmed := strings.TrimSpace(string(data)); trimmed != "" {
					pid = trimmed
				}
			}
			fmt.Printf("✓ YAAT Sidecar is running (PID %s)\n", pid)
			fmt.Printf("  Logs: %s\n", daemon.GetLogPath(logPath))
		} else {
			fmt.Println("✗ YAAT Sidecar is not running")
		}
		os.Exit(0)
	}

	// Handle restart flag
	if *restartService {
		pidPath := getInstancePIDPath(*instanceName)
		logPath := getInstanceLogPath(*instanceName)
		cfg, err := config.LoadConfig(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
			os.Exit(1)
		}
		if daemon.IsRunning(pidPath) {
			if err := daemon.Stop(pidPath); err != nil && !isNotRunningError(err) {
				fmt.Fprintf(os.Stderr, "Failed to stop running sidecar: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("✓ Stopped existing sidecar")
		}
		if err := daemon.Start(cfg.SourcePath, *logFile, pidPath, isVerbose); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to start sidecar: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ Sidecar restarted in background")
		fmt.Printf("  Logs: %s\n", daemon.GetLogPath(logPath))
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
	if err := scrubber.Configure(cfg.Scrubbing); err != nil {
		log.Fatalf("[Sidecar] Failed to configure scrubbing: %v", err)
	}
	resolvedConfigPath := cfg.SourcePath

	// Detect cloud provider and Kubernetes metadata at runtime
	cloudMetadata := detection.DetectCloudProvider()
	k8sMetadata := detection.DetectKubernetesMetadata()

	// Merge detected tags into config (config tags take priority)
	if cfg.Tags == nil {
		cfg.Tags = make(map[string]string)
	}
	if cloudMetadata != nil {
		for k, v := range cloudMetadata.Tags {
			if _, exists := cfg.Tags[k]; !exists {
				cfg.Tags[k] = v
			}
		}
	}
	if k8sMetadata != nil {
		for k, v := range k8sMetadata.Tags {
			if _, exists := cfg.Tags[k]; !exists {
				cfg.Tags[k] = v
			}
		}
	}

	// Handle validate flag
	if *validateCfg {
		fmt.Println("✓ Configuration is valid")
		fmt.Printf("  Service: %s\n", cfg.ServiceName)
		fmt.Printf("  Environment: %s\n", cfg.Environment)
		fmt.Printf("  API Endpoint: %s\n", cfg.APIEndpoint)
		fmt.Printf("  Proxy: %v\n", cfg.Proxy.Enabled)
		fmt.Printf("  Log files: %d\n", len(cfg.Logs))
		fmt.Printf("  Delivery batch size: %d\n", cfg.Delivery.BatchSize)
		fmt.Printf("  Delivery compress: %t\n", cfg.Delivery.Compress)
		fmt.Printf("  Delivery max batch bytes: %d\n", cfg.Delivery.MaxBatchBytes)
		fmt.Printf("  Queue retention: %s\n", cfg.Delivery.QueueRetention)
		fmt.Printf("  Dead-letter retention: %s\n", cfg.Delivery.DeadLetterRetention)
		fmt.Printf("  Host metrics enabled: %t\n", cfg.Metrics.Enabled)
		fmt.Printf("  Host metrics interval: %s\n", cfg.Metrics.Interval)
		fmt.Printf("  StatsD enabled: %t\n", cfg.Metrics.StatsD.Enabled)
		fmt.Printf("  StatsD listen addr: %s\n", cfg.Metrics.StatsD.ListenAddr)

		// Display detected cloud provider and Kubernetes metadata
		if cloudMetadata != nil && cloudMetadata.Provider != "unknown" {
			fmt.Printf("\n  Detected Cloud Provider:\n")
			fmt.Printf("    Provider: %s\n", cloudMetadata.Provider)
			fmt.Printf("    Region: %s\n", cloudMetadata.Region)
			if cloudMetadata.Zone != "" {
				fmt.Printf("    Zone: %s\n", cloudMetadata.Zone)
			}
			if cloudMetadata.InstanceType != "" {
				fmt.Printf("    Instance Type: %s\n", cloudMetadata.InstanceType)
			}
			if cloudMetadata.InstanceID != "" {
				fmt.Printf("    Instance ID: %s\n", cloudMetadata.InstanceID)
			}
		} else {
			fmt.Printf("\n  Cloud Provider: Not detected\n")
		}

		if k8sMetadata != nil && k8sMetadata.InCluster {
			fmt.Printf("\n  Detected Kubernetes:\n")
			fmt.Printf("    Pod Name: %s\n", k8sMetadata.PodName)
			fmt.Printf("    Namespace: %s\n", k8sMetadata.Namespace)
			if k8sMetadata.NodeName != "" {
				fmt.Printf("    Node: %s\n", k8sMetadata.NodeName)
			}
			if k8sMetadata.PodIP != "" {
				fmt.Printf("    Pod IP: %s\n", k8sMetadata.PodIP)
			}
		} else {
			fmt.Printf("\n  Kubernetes: Not detected\n")
		}

		if len(cfg.Tags) > 0 {
			fmt.Printf("\n  Global Tags (%d):\n", len(cfg.Tags))
			for k, v := range cfg.Tags {
				fmt.Printf("    %s: %s\n", k, v)
			}
		}

		os.Exit(0)
	}

	// Handle test flag - test API connection
	if *testAPIFlag {
		fmt.Println("Sending connectivity test events...")
		if len(cfg.Tags) > 0 {
			fmt.Printf("  Including %d global tags in test events\n", len(cfg.Tags))
		}
		fwd := forwarder.NewWithOptions(cfg.APIEndpoint, cfg.APIKey, forwarderOptionsFromConfig(cfg))
		report, err := fwd.Test(cfg.ServiceName, cfg.Environment, cfg.Tags)

		var (
			latency time.Duration
			events  []buffer.Event
		)
		if report != nil {
			latency = report.Latency
			events = report.Events
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ API test failed: %v\n", err)
		} else {
			fmt.Printf("✓ API test succeeded in %v (sent %d events)\n", latency.Truncate(time.Millisecond), len(events))
		}

		if recordErr := state.RecordTestOutcome(cfg.APIEndpoint, cfg.ServiceName, cfg.Environment, events, latency, err); recordErr != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Could not update local state: %v\n", recordErr)
		}

		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Handle daemon mode
	if isDaemon {
		pidPath := getInstancePIDPath(*instanceName)
		logPath := getInstanceLogPath(*instanceName)
		if err := daemon.Start(resolvedConfigPath, *logFile, pidPath, isVerbose); err != nil {
			log.Fatalf("[Sidecar] Failed to start daemon: %v", err)
		}
		fmt.Println("✓ Sidecar started in background")
		fmt.Println("  Check logs with: tail -f", daemon.GetLogPath(logPath))
		fmt.Println("  Manage with: yaat-sidecar --status | --stop | --restart")
		os.Exit(0)
	}

	log.Printf("[Sidecar] YAAT Sidecar v%s starting...", version)
	log.Printf("[Sidecar] Config file: %s", resolvedConfigPath)

	log.Printf("[Sidecar] Service: %s (environment: %s)", cfg.ServiceName, cfg.Environment)
	log.Printf("[Sidecar] API endpoint: %s", cfg.APIEndpoint)
	log.Printf("[Sidecar] Buffer size: %d events", cfg.BufferSize)
	log.Printf("[Sidecar] Flush interval: %v", cfg.FlushIntervalDuration)

	// Log detected cloud provider and Kubernetes metadata
	if cloudMetadata != nil && cloudMetadata.Provider != "unknown" {
		log.Printf("[Sidecar] Cloud provider: %s (region: %s, instance: %s)",
			cloudMetadata.Provider, cloudMetadata.Region, cloudMetadata.InstanceID)
	}
	if k8sMetadata != nil && k8sMetadata.InCluster {
		log.Printf("[Sidecar] Kubernetes: pod=%s, namespace=%s, node=%s",
			k8sMetadata.PodName, k8sMetadata.Namespace, k8sMetadata.NodeName)
	}
	if len(cfg.Tags) > 0 {
		log.Printf("[Sidecar] Global tags: %d configured", len(cfg.Tags))
	}

	// Initialize analytics writer
	var analyticsWriter *analytics.Writer
	if cfg.Analytics.Enabled {
		// Determine organization ID for local storage
		orgID := cfg.OrganizationID
		if orgID == "" {
			orgID = "local" // Local-only mode
		}

		aw, err := analytics.NewWriter(analytics.Config{
			DatabasePath:   cfg.Analytics.DatabasePath,
			OrganizationID: orgID,
			ServiceName:    cfg.ServiceName,
			Environment:    cfg.Environment,
			RetentionDays:  cfg.Analytics.RetentionDays,
			MaxSizeGB:      cfg.Analytics.MaxSizeGB,
			BatchSize:      cfg.Analytics.BatchSize,
			WriteTimeout:   cfg.Analytics.TimeoutDuration,
		})
		if err != nil {
			log.Printf("[Analytics] Failed to initialize: %v. Continuing without local analytics.", err)
		} else {
			analyticsWriter = aw
			defer analyticsWriter.Close()

			// Start retention cleanup (runs daily at 3am)
			analyticsWriter.StartRetentionCleanup(24 * time.Hour)

			// Log mode
			mode := "cloud + local"
			if cfg.APIKey == "" {
				mode = "local-only"
			}
			log.Printf("[Analytics] Enabled (%s): %s", mode, cfg.Analytics.DatabasePath)
		}
	}

	// Create event buffer
	buf := buffer.New(cfg.BufferSize)

	// Persistent queue
	queueDir := queue.DefaultDir()
	if envQueue := os.Getenv("YAAT_QUEUE_DIR"); envQueue != "" {
		queueDir = envQueue
	}
	queueStore, err := queue.New(queueDir)
	if err != nil {
		log.Printf("[Sidecar] Warning: failed to initialize persistent queue: %v", err)
	}

	updateQueueMetrics(buf, queueStore)

	var stopMetrics func()
	var stopStatsd func()
	if cfg.Metrics.Enabled {
		collector, err := metrics.NewCollector(cfg.OrganizationID, cfg.ServiceName, cfg.Environment, cfg.Tags, cfg.Metrics, buf)
		if err != nil {
			log.Printf("[Sidecar] Host metrics disabled: %v", err)
		} else {
			stopMetrics = collector.Start()
			log.Printf("[Sidecar] Host metrics collector running (interval %v)", cfg.Metrics.IntervalDuration)
		}
		if cfg.Metrics.StatsD.Enabled {
			statsdCfg := cfg.Metrics.StatsD
			if len(cfg.Metrics.Tags) > 0 {
				if statsdCfg.Tags == nil {
					statsdCfg.Tags = make(map[string]string, len(cfg.Metrics.Tags))
				}
				for k, v := range cfg.Metrics.Tags {
					if _, exists := statsdCfg.Tags[k]; !exists {
						statsdCfg.Tags[k] = v
					}
				}
			}
			statsdServer := statsd.New(statsdCfg, cfg.OrganizationID, cfg.ServiceName, cfg.Environment, cfg.Tags, buf)
			stop, err := statsdServer.Start()
			if err != nil {
				log.Printf("[Sidecar] StatsD listener disabled: %v", err)
			} else {
				stopStatsd = stop
				log.Printf("[Sidecar] StatsD listener running on %s", cfg.Metrics.StatsD.ListenAddr)
			}
		}
	}

	// Create forwarder
	fwd := forwarder.NewWithOptions(cfg.APIEndpoint, cfg.APIKey, forwarderOptionsFromConfig(cfg))

	// Start periodic flusher
	stopFlusher := make(chan struct{})
	go periodicFlusher(buf, fwd, cfg.FlushIntervalDuration, stopFlusher, queueStore, cfg.Delivery.QueueRetentionDuration, cfg.Delivery.DeadLetterRetentionDuration, analyticsWriter, cfg.APIKey)

	// Start log tailers
	var journaldTailers []*logs.JournaldTailer
	if len(cfg.Logs) > 0 {
		log.Printf("[Sidecar] Starting %d log tailers...", len(cfg.Logs))
		for _, logCfg := range cfg.Logs {
			format := strings.ToLower(logCfg.Format)
			if format == "journald" {
				tailer := logs.NewJournaldTailer(cfg.OrganizationID, cfg.ServiceName, cfg.Environment, cfg.Tags, buf)
				if err := tailer.Start(logCfg.Path); err != nil {
					log.Printf("[Sidecar] Failed to start journald tailer (%s): %v", logCfg.Path, err)
				} else {
					journaldTailers = append(journaldTailers, tailer)
					log.Printf("[Sidecar] Streaming journald entries (match: %s)", logCfg.Path)
				}
				continue
			}

			tailer := logs.New(logCfg.Path, logCfg.Format, cfg.OrganizationID, cfg.ServiceName, cfg.Environment, cfg.Tags, buf)
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
			cfg.OrganizationID,
			cfg.ServiceName,
			cfg.Environment,
			cfg.Tags,
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
		healthSvc := health.New(*healthPort, version, cfg.ServiceName, func() diag.Snapshot {
			return diag.Global().Snapshot()
		})
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

	if stopMetrics != nil {
		stopMetrics()
	}
	if stopStatsd != nil {
		stopStatsd()
	}
	for _, tailer := range journaldTailers {
		tailer.Stop()
	}

	// Flush remaining events
	updateQueueMetrics(buf, queueStore)
	events := buf.Flush()
	updateQueueMetrics(buf, queueStore)
	if len(events) > 0 {
		log.Printf("[Sidecar] Flushing %d remaining events...", len(events))

		// Write to local analytics
		if analyticsWriter != nil {
			if err := analyticsWriter.Write(events); err != nil {
				log.Printf("[Analytics] Shutdown write failed: %v", err)
			}
		}

		// Forward to cloud (only if api_key is set)
		if cfg.APIKey != "" {
			if err := fwd.Send(events); err != nil {
				log.Printf("[Sidecar] Failed to flush events: %v", err)
				diag.Global().RecordSendFailure(err, len(events))
				if queueStore != nil {
					if enqueueErr := queueStore.Enqueue(events); enqueueErr != nil {
						log.Printf("[Sidecar] Failed to enqueue events to persistent queue: %v", enqueueErr)
					}
				}
			} else {
				diag.Global().RecordSendSuccess(len(events))
			}
		}
	}
	updateQueueMetrics(buf, queueStore)

	log.Printf("[Sidecar] Shutdown complete.")
}

// periodicFlusher flushes the buffer periodically
func periodicFlusher(buf *buffer.Buffer, fwd *forwarder.Forwarder, interval time.Duration, stop chan struct{}, store *queue.Storage, queueRetention, dlqRetention time.Duration, analyticsWriter *analytics.Writer, apiKey string) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	drainPersistentQueue(store, fwd)
	updateQueueMetrics(buf, store)
	cleanupQueues(store, queueRetention, dlqRetention)

	for {
		select {
		case <-ticker.C:
			drainPersistentQueue(store, fwd)
			updateQueueMetrics(buf, store)
			events := buf.Flush()
			updateQueueMetrics(buf, store)
			cleanupQueues(store, queueRetention, dlqRetention)
			if len(events) == 0 {
				continue
			}

			log.Printf("[Flusher] Flushing %d events...", len(events))

			// Write to local analytics (async, non-blocking)
			if analyticsWriter != nil {
				if err := analyticsWriter.Write(events); err != nil {
					log.Printf("[Analytics] Write failed: %v", err)
				}
			}

			// Forward to cloud API (only if api_key is set)
			if apiKey != "" {
				if err := fwd.Send(events); err != nil {
					log.Printf("[Flusher] Failed to send events: %v", err)
					diag.Global().RecordSendFailure(err, len(events))
					if store != nil {
						if enqueueErr := store.Enqueue(events); enqueueErr != nil {
							log.Printf("[Flusher] Failed to enqueue events to persistent queue: %v", enqueueErr)
						}
						updateQueueMetrics(buf, store)
					}
				} else {
					diag.Global().RecordSendSuccess(len(events))
				}
			} else {
				// Local-only mode - no cloud forwarding
				log.Printf("[Flusher] Local-only mode: %d events stored locally", len(events))
			}

		case <-stop:
			log.Printf("[Flusher] Stopped")
			return
		}
	}
}

func updateQueueMetrics(buf *buffer.Buffer, store *queue.Storage) {
	inMemory := 0
	if buf != nil {
		inMemory = buf.Len()
	}

	if store == nil {
		diag.Global().SetQueueState(inMemory, 0, 0)
		return
	}
	pending, err := store.Pending()
	if err != nil {
		log.Printf("[Sidecar] Failed to inspect persistent queue: %v", err)
		pending = 0
	}
	deadLetter, err := store.DeadLetterPending()
	if err != nil {
		log.Printf("[Sidecar] Failed to inspect deadletter queue: %v", err)
		deadLetter = 0
	}
	diag.Global().SetQueueState(inMemory, pending, deadLetter)
}

func cleanupQueues(store *queue.Storage, queueRetention, dlqRetention time.Duration) {
	if store == nil {
		return
	}
	if queueRetention <= 0 && dlqRetention <= 0 {
		return
	}
	if err := store.Cleanup(queueRetention, dlqRetention); err != nil {
		log.Printf("[Sidecar] Failed to cleanup queue storage: %v", err)
	}
}

func drainPersistentQueue(store *queue.Storage, fwd *forwarder.Forwarder) {
	if store == nil {
		return
	}

	for {
		token, events, err := store.Dequeue()
		if err != nil {
			log.Printf("[Flusher] Failed to dequeue persistent batch: %v", err)
			return
		}
		if events == nil {
			return
		}

		if err := fwd.Send(events); err != nil {
			log.Printf("[Flusher] Failed to send persisted batch: %v", err)
			diag.Global().RecordSendFailure(err, len(events))
			if moveErr := store.MoveToDLQ(token); moveErr != nil {
				log.Printf("[Flusher] Failed to move batch to DLQ: %v", moveErr)
			}
			updateQueueMetrics(nil, store)
			return
		}

		diag.Global().RecordSendSuccess(len(events))
		if ackErr := store.Ack(token); ackErr != nil {
			log.Printf("[Flusher] Failed to ack batch: %v", ackErr)
		}
		updateQueueMetrics(nil, store)
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
	default:
		// Linux-only sidecar
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

func forwarderOptionsFromConfig(cfg *config.Config) forwarder.Options {
	return forwarder.Options{
		BatchSize:     cfg.Delivery.BatchSize,
		Compress:      cfg.Delivery.Compress,
		MaxBatchBytes: cfg.Delivery.MaxBatchBytes,
	}
}

// getInstancePIDPath returns the instance-specific PID file path
func getInstancePIDPath(instance string) string {
	if instance == "default" {
		return "/var/run/yaat-sidecar.pid"
	}
	return fmt.Sprintf("/var/run/yaat-%s.pid", instance)
}

// getInstanceLogPath returns the instance-specific log file path
func getInstanceLogPath(instance string) string {
	if instance == "default" {
		return "/var/log/yaat-sidecar.log"
	}
	return fmt.Sprintf("/var/log/yaat-%s.log", instance)
}

// getInstanceConfigPath returns the instance-specific config path
func getInstanceConfigPath(instance, configPath string) string {
	// If user explicitly provided a config path, use it as-is
	if configPath != "yaat.yaml" {
		return configPath
	}

	// Otherwise, use instance-specific path
	if instance == "default" {
		return configPath // Use the default "yaat.yaml"
	}
	return fmt.Sprintf("%s.yaml", instance)
}

// getInstanceQueueDir returns the instance-specific queue directory
func getInstanceQueueDir(instance string) string {
	if instance == "default" {
		return "/var/lib/yaat/queue"
	}
	return fmt.Sprintf("/var/lib/yaat/%s/queue", instance)
}

// getInstanceStateDir returns the instance-specific state directory
func getInstanceStateDir(instance string) string {
	if instance == "default" {
		return "/var/lib/yaat/state"
	}
	return fmt.Sprintf("/var/lib/yaat/%s/state", instance)
}
