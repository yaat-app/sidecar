package diag

import (
	"sync"
	"time"
)

// Snapshot represents a read-only view of diagnostic metrics.
type Snapshot struct {
	CollectedAt       time.Time `json:"collected_at"`
	InMemoryQueue     int       `json:"in_memory_queue"`
	PersistedQueue    int       `json:"persisted_queue"`
	DeadLetterQueue   int       `json:"dead_letter_queue"`
	QueueLength       int       `json:"queue_length"`
	LastSuccessAt     time.Time `json:"last_success_at"`
	LastFailureAt     time.Time `json:"last_failure_at"`
	LastError         string    `json:"last_error"`
	TotalEventsSent   int64     `json:"total_events_sent"`
	TotalEventsFailed int64     `json:"total_events_failed"`
	ThroughputPerMin  float64   `json:"throughput_per_min"`
}

// State tracks runtime diagnostics.
type State struct {
	mu       sync.RWMutex
	snapshot Snapshot
	history  []sendSample
}

type sendSample struct {
	at    time.Time
	count int
}

var (
	global = &State{}
)

// Global returns the shared diagnostics state.
func Global() *State {
	return global
}

// Snapshot returns a copy of the current state.
func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot
}

// SetQueueState records the current queue lengths.
func (s *State) SetQueueState(inMemory, persisted, deadLetter int) {
	s.mu.Lock()
	s.snapshot.InMemoryQueue = inMemory
	s.snapshot.PersistedQueue = persisted
	s.snapshot.DeadLetterQueue = deadLetter
	total := inMemory + persisted
	if total < 0 {
		total = 0
	}
	s.snapshot.QueueLength = total
	s.snapshot.CollectedAt = time.Now().UTC()
	s.mu.Unlock()
}

// RecordSendSuccess updates metrics after a successful send.
func (s *State) RecordSendSuccess(events int) {
	now := time.Now().UTC()
	s.mu.Lock()
	s.snapshot.LastSuccessAt = now
	s.snapshot.LastError = ""
	s.snapshot.TotalEventsSent += int64(events)
	s.appendSampleLocked(now, events)
	s.snapshot.CollectedAt = now
	s.snapshot.ThroughputPerMin = s.calculateThroughputLocked(now)
	s.mu.Unlock()
}

// RecordSendFailure tracks a failed send attempt.
func (s *State) RecordSendFailure(err error, events int) {
	now := time.Now().UTC()
	s.mu.Lock()
	s.snapshot.LastFailureAt = now
	if err != nil {
		s.snapshot.LastError = err.Error()
	}
	if events > 0 {
		s.snapshot.TotalEventsFailed += int64(events)
	}
	s.pruneHistoryLocked(now)
	s.snapshot.CollectedAt = now
	s.mu.Unlock()
}

func (s *State) appendSampleLocked(now time.Time, count int) {
	if count <= 0 {
		return
	}
	s.history = append(s.history, sendSample{
		at:    now,
		count: count,
	})
	s.pruneHistoryLocked(now)
}

func (s *State) pruneHistoryLocked(now time.Time) {
	cutoff := now.Add(-1 * time.Minute)
	var kept []sendSample
	for _, sample := range s.history {
		if sample.at.After(cutoff) {
			kept = append(kept, sample)
		}
	}
	s.history = kept
}

func (s *State) calculateThroughputLocked(now time.Time) float64 {
	if len(s.history) == 0 {
		return 0
	}
	cutoff := now.Add(-1 * time.Minute)
	var total int
	for _, sample := range s.history {
		if sample.at.After(cutoff) {
			total += sample.count
		}
	}
	return float64(total)
}
