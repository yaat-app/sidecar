package analytics

import (
	"fmt"
	"log"
	"time"
)

const (
	// Buffer percentage when doing aggressive cleanup
	diskCleanupBuffer = 0.10 // 10%
)

// StartRetentionCleanup starts a background goroutine that periodically cleans up old events
func (w *Writer) StartRetentionCleanup(interval time.Duration) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Run cleanup immediately on start
		if err := w.runRetentionCleanup(); err != nil {
			log.Printf("[Analytics] Initial retention cleanup failed: %v", err)
		}

		for {
			select {
			case <-ticker.C:
				if err := w.runRetentionCleanup(); err != nil {
					log.Printf("[Analytics] Retention cleanup failed: %v", err)
				}

			case <-w.closeChan:
				return
			}
		}
	}()
}

// runRetentionCleanup performs the actual cleanup logic
func (w *Writer) runRetentionCleanup() error {
	// Step 1: Delete events older than retention period
	if err := w.deleteOldEvents(); err != nil {
		return fmt.Errorf("failed to delete old events: %w", err)
	}

	// Step 2: Check disk usage and perform aggressive cleanup if needed
	if err := w.enforceMaxSize(); err != nil {
		return fmt.Errorf("failed to enforce max size: %w", err)
	}

	// Step 3: Vacuum database to reclaim space
	if err := w.vacuumDatabase(); err != nil {
		log.Printf("[Analytics] Vacuum failed (non-fatal): %v", err)
		// Don't return error - vacuum is a best-effort operation
	}

	return nil
}

// deleteOldEvents deletes events older than the retention period
func (w *Writer) deleteOldEvents() error {
	if w.config.RetentionDays <= 0 {
		return nil // Retention disabled
	}

	cutoffTime := time.Now().UTC().AddDate(0, 0, -w.config.RetentionDays)

	result, err := w.db.Exec("DELETE FROM events WHERE timestamp < ?", cutoffTime)
	if err != nil {
		return err
	}

	rowsDeleted, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsDeleted > 0 {
		log.Printf("[Analytics] Retention cleanup: deleted %d events older than %dd",
			rowsDeleted, w.config.RetentionDays)
	}

	return nil
}

// enforceMaxSize deletes oldest events if database exceeds max size
func (w *Writer) enforceMaxSize() error {
	if w.config.MaxSizeGB <= 0 {
		return nil // Size limit disabled
	}

	sizeBytes, err := w.GetDatabaseSize()
	if err != nil {
		return fmt.Errorf("failed to get database size: %w", err)
	}

	currentSizeGB := float64(sizeBytes) / 1e9
	maxSizeBytes := int64(w.config.MaxSizeGB * 1e9)

	if sizeBytes <= maxSizeBytes {
		return nil // Under limit
	}

	// Calculate how much to delete (current - max + 10% buffer)
	excessBytes := sizeBytes - maxSizeBytes
	bufferBytes := int64(float64(maxSizeBytes) * diskCleanupBuffer)
	targetDeleteBytes := excessBytes + bufferBytes

	// Estimate events to delete (assuming ~1KB per event)
	estimatedEventSize := int64(1024)
	eventsToDelete := targetDeleteBytes / estimatedEventSize

	log.Printf("[Analytics] Database size %.2fGB exceeds limit %.2fGB, deleting ~%d oldest events",
		currentSizeGB, w.config.MaxSizeGB, eventsToDelete)

	// Delete oldest events
	result, err := w.db.Exec(`
		DELETE FROM events
		WHERE event_id IN (
			SELECT event_id
			FROM events
			ORDER BY timestamp ASC
			LIMIT ?
		)
	`, eventsToDelete)
	if err != nil {
		return err
	}

	rowsDeleted, err := result.RowsAffected()
	if err != nil {
		return err
	}

	log.Printf("[Analytics] Aggressive cleanup: deleted %d events to free space", rowsDeleted)

	return nil
}

// vacuumDatabase runs VACUUM to reclaim disk space
func (w *Writer) vacuumDatabase() error {
	_, err := w.db.Exec("VACUUM")
	if err != nil {
		return err
	}

	log.Printf("[Analytics] Database vacuumed")
	return nil
}

// GetRetentionStats returns statistics about the database
type RetentionStats struct {
	TotalEvents    int64
	OldestEvent    time.Time
	NewestEvent    time.Time
	DatabaseSizeGB float64
}

// GetRetentionStats returns current retention statistics
func (w *Writer) GetRetentionStats() (RetentionStats, error) {
	stats := RetentionStats{}

	// Get total events
	row := w.db.QueryRow("SELECT COUNT(*) FROM events")
	if err := row.Scan(&stats.TotalEvents); err != nil {
		return stats, err
	}

	// Get oldest and newest event timestamps
	if stats.TotalEvents > 0 {
		row = w.db.QueryRow("SELECT MIN(timestamp), MAX(timestamp) FROM events")
		if err := row.Scan(&stats.OldestEvent, &stats.NewestEvent); err != nil {
			return stats, err
		}
	}

	// Get database size
	sizeBytes, err := w.GetDatabaseSize()
	if err != nil {
		return stats, err
	}
	stats.DatabaseSizeGB = float64(sizeBytes) / 1e9

	return stats, nil
}
