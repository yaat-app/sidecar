package analytics

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/duckdb/duckdb-go/v2" // DuckDB driver
	"github.com/yaat-app/sidecar/internal/buffer"
)

const (
	// Default values
	defaultBatchSize    = 500
	defaultWriteTimeout = 5 * time.Second
	defaultQueueDepth   = 10 // Buffer 10 batches

	// Truncation limits
	maxMessageSize    = 100_000 // 100KB
	maxStacktraceSize = 50_000  // 50KB

	// Retry configuration
	maxRetries     = 3
	initialBackoff = time.Second
)

// Config holds analytics writer configuration
type Config struct {
	DatabasePath   string
	OrganizationID string // "local" or actual org ID
	ServiceName    string
	Environment    string
	RetentionDays  int
	MaxSizeGB      float64
	BatchSize      int
	WriteTimeout   time.Duration
}

// Writer handles async writes to DuckDB analytics database
type Writer struct {
	db             *sql.DB
	config         Config
	queue          chan []buffer.Event
	closeChan      chan struct{}
	wg             sync.WaitGroup
	insertStmt     *sql.Stmt
	insertStmtLock sync.Mutex

	// Metrics
	totalWritten  int64
	totalDropped  int64
	lastWriteTime atomic.Value // time.Time
}

// Stats holds writer statistics
type Stats struct {
	TotalWritten  int64
	TotalDropped  int64
	QueueDepth    int
	LastWriteTime time.Time
}

// NewWriter creates a new analytics writer
func NewWriter(cfg Config) (*Writer, error) {
	// Apply defaults
	if cfg.BatchSize == 0 {
		cfg.BatchSize = defaultBatchSize
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = defaultWriteTimeout
	}

	// Expand tilde in database path
	dbPath := cfg.DatabasePath
	if len(dbPath) > 0 && dbPath[0] == '~' {
		home := os.Getenv("HOME")
		if home == "" {
			return nil, fmt.Errorf("cannot expand ~ in path: HOME not set")
		}
		dbPath = filepath.Join(home, dbPath[1:])
	}

	// Create database directory if it doesn't exist
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open database with single writer connection
	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool (single writer)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	// Initialize schema
	if err := initializeSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	// Prepare insert statement
	insertSQL := `
		INSERT INTO events (
			organization_id, service_name, event_id, timestamp, received_at,
			event_type, level, message, stacktrace,
			trace_id, span_id, parent_span_id, operation, duration_ms, status_code,
			metric_name, metric_value,
			environment, tags
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	stmt, err := db.Prepare(insertSQL)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to prepare insert statement: %w", err)
	}

	w := &Writer{
		db:         db,
		config:     cfg,
		queue:      make(chan []buffer.Event, defaultQueueDepth),
		closeChan:  make(chan struct{}),
		insertStmt: stmt,
	}

	// Start async writer goroutine
	w.wg.Add(1)
	go w.processQueue()

	log.Printf("[Analytics] Initialized: %s (retention: %dd, max: %.1fGB)",
		dbPath, cfg.RetentionDays, cfg.MaxSizeGB)

	return w, nil
}

// Write asynchronously writes events to the analytics database
func (w *Writer) Write(events []buffer.Event) error {
	if len(events) == 0 {
		return nil
	}

	select {
	case w.queue <- events:
		return nil
	default:
		// Queue full - drop events
		dropped := int64(len(events))
		atomic.AddInt64(&w.totalDropped, dropped)
		return fmt.Errorf("analytics queue full, dropped %d events", dropped)
	}
}

// processQueue runs in a goroutine and processes the write queue
func (w *Writer) processQueue() {
	defer w.wg.Done()

	for {
		select {
		case events := <-w.queue:
			if err := w.writeBatchWithRetry(events); err != nil {
				log.Printf("[Analytics] Failed to write batch: %v", err)
				atomic.AddInt64(&w.totalDropped, int64(len(events)))
			} else {
				atomic.AddInt64(&w.totalWritten, int64(len(events)))
				w.lastWriteTime.Store(time.Now())
			}

		case <-w.closeChan:
			// Drain remaining events
			for {
				select {
				case events := <-w.queue:
					if err := w.writeBatchWithRetry(events); err != nil {
						log.Printf("[Analytics] Failed to write batch during shutdown: %v", err)
					}
				default:
					return
				}
			}
		}
	}
}

// writeBatchWithRetry writes a batch of events with retry logic
func (w *Writer) writeBatchWithRetry(events []buffer.Event) error {
	backoff := initialBackoff

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(backoff)
			backoff *= 2 // Exponential backoff
		}

		err := w.writeBatch(events)
		if err == nil {
			return nil
		}

		// Don't retry on certain errors
		if err == sql.ErrConnDone {
			return fmt.Errorf("database closed: %w", err)
		}

		log.Printf("[Analytics] Write attempt %d/%d failed: %v", attempt+1, maxRetries, err)
	}

	return fmt.Errorf("failed after %d retries", maxRetries)
}

// writeBatch writes a batch of events in a single transaction
func (w *Writer) writeBatch(events []buffer.Event) error {
	tx, err := w.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() // No-op if committed

	stmt := tx.Stmt(w.insertStmt)
	defer stmt.Close()

	for _, event := range events {
		// Truncate large fields
		truncateEventFields(event)

		// Extract and convert fields
		orgID := w.stringOrDefault(event["organization_id"], w.config.OrganizationID)
		serviceName := w.stringOrDefault(event["service_name"], w.config.ServiceName)
		eventID := w.stringOrDefault(event["event_id"], "")
		timestamp := w.timeOrNow(event["timestamp"])
		receivedAt := w.timeOrNow(event["received_at"])
		eventType := w.stringOrDefault(event["event_type"], "")
		level := w.stringOrDefault(event["level"], "")
		message := w.stringOrDefault(event["message"], "")
		stacktrace := w.stringOrDefault(event["stacktrace"], "")
		traceID := w.stringOrDefault(event["trace_id"], "")
		spanID := w.stringOrDefault(event["span_id"], "")
		parentSpanID := w.stringOrDefault(event["parent_span_id"], "")
		operation := w.stringOrDefault(event["operation"], "")
		durationMs := w.float64OrZero(event["duration_ms"])
		statusCode := w.intOrZero(event["status_code"])
		metricName := w.stringOrDefault(event["metric_name"], "")
		metricValue := w.float64OrZero(event["metric_value"])
		environment := w.stringOrDefault(event["environment"], w.config.Environment)

		// Convert tags to JSON string
		tagsJSON := w.convertTagsToJSON(event["tags"])

		_, err := stmt.Exec(
			orgID, serviceName, eventID, timestamp, receivedAt,
			eventType, level, message, stacktrace,
			traceID, spanID, parentSpanID, operation, durationMs, statusCode,
			metricName, metricValue,
			environment, tagsJSON,
		)
		if err != nil {
			return fmt.Errorf("failed to insert event %s: %w", eventID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// truncateEventFields truncates large message and stacktrace fields
func truncateEventFields(event buffer.Event) {
	if msg, ok := event["message"].(string); ok && len(msg) > maxMessageSize {
		event["message"] = msg[:maxMessageSize] + "...[TRUNCATED]"
	}

	if stack, ok := event["stacktrace"].(string); ok && len(stack) > maxStacktraceSize {
		event["stacktrace"] = stack[:maxStacktraceSize] + "...[TRUNCATED]"
	}
}

// Helper functions for type conversion

func (w *Writer) stringOrDefault(val interface{}, def string) string {
	if s, ok := val.(string); ok {
		return s
	}
	return def
}

func (w *Writer) timeOrNow(val interface{}) time.Time {
	switch v := val.(type) {
	case time.Time:
		return v
	case string:
		// Try to parse RFC3339
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t
		}
	}
	return time.Now().UTC()
}

func (w *Writer) float64OrZero(val interface{}) float64 {
	switch v := val.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0.0
}

func (w *Writer) intOrZero(val interface{}) int {
	switch v := val.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case int32:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

func (w *Writer) convertTagsToJSON(val interface{}) string {
	// Convert tags map to JSON string for DuckDB VARCHAR column
	tagsMap, ok := val.(map[string]string)
	if !ok || len(tagsMap) == 0 {
		return "{}"
	}

	jsonBytes, err := json.Marshal(tagsMap)
	if err != nil {
		log.Printf("[Analytics] Failed to marshal tags: %v", err)
		return "{}"
	}
	return string(jsonBytes)
}

// Stats returns current writer statistics
func (w *Writer) Stats() Stats {
	var lastWrite time.Time
	if val := w.lastWriteTime.Load(); val != nil {
		lastWrite = val.(time.Time)
	}

	return Stats{
		TotalWritten:  atomic.LoadInt64(&w.totalWritten),
		TotalDropped:  atomic.LoadInt64(&w.totalDropped),
		QueueDepth:    len(w.queue),
		LastWriteTime: lastWrite,
	}
}

// Close gracefully shuts down the writer
func (w *Writer) Close() error {
	// Signal shutdown
	close(w.closeChan)

	// Wait for goroutine to finish
	w.wg.Wait()

	// Close statement and database
	if w.insertStmt != nil {
		w.insertStmt.Close()
	}

	if w.db != nil {
		return w.db.Close()
	}

	return nil
}

// GetDatabaseSize returns the current database size in bytes
func (w *Writer) GetDatabaseSize() (int64, error) {
	var sizeBytes int64
	row := w.db.QueryRow("SELECT SUM(total_blocks * block_size) FROM pragma_database_size()")
	if err := row.Scan(&sizeBytes); err != nil {
		return 0, err
	}
	return sizeBytes, nil
}
