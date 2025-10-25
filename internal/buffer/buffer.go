package buffer

import (
	"sync"
)

// Event represents a single event to be sent to YAAT
type Event map[string]interface{}

// Buffer holds events in memory until flushed
type Buffer struct {
	mu     sync.Mutex
	events []Event
	size   int
}

// New creates a new Buffer with the specified maximum size
func New(size int) *Buffer {
	return &Buffer{
		events: make([]Event, 0, size),
		size:   size,
	}
}

// Add adds an event to the buffer
// Returns true if buffer is full and should be flushed
func (b *Buffer) Add(event Event) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.events = append(b.events, event)
	return len(b.events) >= b.size
}

// Flush returns all buffered events and clears the buffer
func (b *Buffer) Flush() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.events) == 0 {
		return nil
	}

	// Copy events
	events := make([]Event, len(b.events))
	copy(events, b.events)

	// Clear buffer
	b.events = b.events[:0]

	return events
}

// Len returns the current number of buffered events
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.events)
}
