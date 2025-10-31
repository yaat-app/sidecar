package analytics

import (
	"database/sql"
	"fmt"
)

// DDL for events table - matches ClickHouse schema exactly
const createEventsTableSQL = `
CREATE TABLE IF NOT EXISTS events (
    -- Core identifiers
    organization_id VARCHAR NOT NULL,
    service_name VARCHAR NOT NULL,
    event_id VARCHAR PRIMARY KEY,

    -- Timestamps
    timestamp TIMESTAMP WITH TIME ZONE NOT NULL,
    received_at TIMESTAMP WITH TIME ZONE NOT NULL,

    -- Event classification
    event_type VARCHAR NOT NULL CHECK (event_type IN ('log', 'span', 'metric')),

    -- Log fields (populated when event_type='log')
    level VARCHAR CHECK (level IN ('', 'debug', 'info', 'warning', 'error', 'critical')),
    message VARCHAR,
    stacktrace VARCHAR,

    -- Span fields (populated when event_type='span')
    trace_id VARCHAR,
    span_id VARCHAR,
    parent_span_id VARCHAR,
    operation VARCHAR,
    duration_ms DOUBLE,
    status_code USMALLINT,

    -- Metric fields (populated when event_type='metric')
    metric_name VARCHAR,
    metric_value DOUBLE,

    -- Common fields
    environment VARCHAR NOT NULL,
    tags VARCHAR NOT NULL DEFAULT '{}'
);
`

// Performance indexes
const createIndexesSQL = `
CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);
CREATE INDEX IF NOT EXISTS idx_events_event_type ON events(event_type);
CREATE INDEX IF NOT EXISTS idx_events_org_service ON events(organization_id, service_name);
CREATE INDEX IF NOT EXISTS idx_events_level ON events(level);
CREATE INDEX IF NOT EXISTS idx_events_trace_id ON events(trace_id);
`

// Schema version tracking for migrations
const createSchemaVersionTableSQL = `
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
`

// schemaVersion is the current schema version
const schemaVersion = 1

// initializeSchema creates the database schema if it doesn't exist
func initializeSchema(db *sql.DB) error {
	// Create schema_version table first
	if _, err := db.Exec(createSchemaVersionTableSQL); err != nil {
		return fmt.Errorf("failed to create schema_version table: %w", err)
	}

	// Check current schema version
	var currentVersion int
	row := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version")
	if err := row.Scan(&currentVersion); err != nil {
		return fmt.Errorf("failed to query schema version: %w", err)
	}

	// If schema already at target version, nothing to do
	if currentVersion >= schemaVersion {
		return nil
	}

	// Apply migrations
	if currentVersion == 0 {
		// Initial schema creation
		if err := applyMigrationV1(db); err != nil {
			return fmt.Errorf("failed to apply migration v1: %w", err)
		}
	}

	// Future migrations would go here:
	// if currentVersion < 2 {
	//     if err := applyMigrationV2(db); err != nil {
	//         return err
	//     }
	// }

	return nil
}

// applyMigrationV1 creates the initial schema
func applyMigrationV1(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() // No-op if committed

	// Create events table
	if _, err := tx.Exec(createEventsTableSQL); err != nil {
		return fmt.Errorf("failed to create events table: %w", err)
	}

	// Create indexes
	if _, err := tx.Exec(createIndexesSQL); err != nil {
		return fmt.Errorf("failed to create indexes: %w", err)
	}

	// Record migration
	if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (?)", 1); err != nil {
		return fmt.Errorf("failed to record schema version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit migration: %w", err)
	}

	return nil
}

// getSchemaVersion returns the current schema version
func getSchemaVersion(db *sql.DB) (int, error) {
	var version int
	row := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version")
	if err := row.Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}
