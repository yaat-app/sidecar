package buffer

import (
	"testing"
)

func TestNewBuffer(t *testing.T) {
	size := 100
	buf := New(size)

	if buf == nil {
		t.Fatal("New() returned nil")
	}

	if buf.size != size {
		t.Errorf("Expected size %d, got %d", size, buf.size)
	}

	if buf.Len() != 0 {
		t.Errorf("Expected initial length 0, got %d", buf.Len())
	}
}

func TestAddEvent(t *testing.T) {
	buf := New(10)

	event := Event{
		"event_id":     "test-123",
		"service_name": "test-service",
		"timestamp":    "2024-10-25T12:00:00Z",
	}

	// Add event
	shouldFlush := buf.Add(event)

	if shouldFlush {
		t.Error("Expected shouldFlush to be false after adding 1 event to buffer of size 10")
	}

	if buf.Len() != 1 {
		t.Errorf("Expected length 1 after adding event, got %d", buf.Len())
	}
}

func TestBufferFull(t *testing.T) {
	size := 5
	buf := New(size)

	// Add events until full
	for i := 0; i < size-1; i++ {
		event := Event{"id": i}
		shouldFlush := buf.Add(event)
		if shouldFlush {
			t.Errorf("Buffer should not be full after %d events (size: %d)", i+1, size)
		}
	}

	// Add final event that fills the buffer
	finalEvent := Event{"id": size - 1}
	shouldFlush := buf.Add(finalEvent)

	if !shouldFlush {
		t.Error("Expected shouldFlush to be true when buffer is full")
	}

	if buf.Len() != size {
		t.Errorf("Expected buffer length %d, got %d", size, buf.Len())
	}
}

func TestFlush(t *testing.T) {
	buf := New(10)

	// Add some events
	event1 := Event{"id": "1", "name": "first"}
	event2 := Event{"id": "2", "name": "second"}
	event3 := Event{"id": "3", "name": "third"}

	buf.Add(event1)
	buf.Add(event2)
	buf.Add(event3)

	// Flush
	events := buf.Flush()

	if len(events) != 3 {
		t.Errorf("Expected 3 events after flush, got %d", len(events))
	}

	// Buffer should be empty after flush
	if buf.Len() != 0 {
		t.Errorf("Expected buffer to be empty after flush, got length %d", buf.Len())
	}

	// Verify event content
	if events[0]["id"] != "1" {
		t.Errorf("Expected first event id '1', got '%v'", events[0]["id"])
	}
}

func TestFlushEmpty(t *testing.T) {
	buf := New(10)

	// Flush empty buffer
	events := buf.Flush()

	if events != nil {
		t.Errorf("Expected nil from flushing empty buffer, got %v", events)
	}
}

func TestConcurrentAccess(t *testing.T) {
	buf := New(1000)
	done := make(chan bool)

	// Concurrent adds
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				buf.Add(Event{"id": id*100 + j})
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should have 1000 events
	if buf.Len() != 1000 {
		t.Errorf("Expected 1000 events from concurrent access, got %d", buf.Len())
	}
}
