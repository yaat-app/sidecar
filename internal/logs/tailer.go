package logs

import (
	"log"
	"strings"

	"github.com/hpcloud/tail"
	"github.com/yaat-app/sidecar/internal/buffer"
	"github.com/yaat-app/sidecar/internal/scrubber"
)

// Tailer tails a log file and parses lines
type Tailer struct {
	path           string
	format         string
	organizationID string
	serviceName    string
	environment    string
	globalTags     map[string]string
	buffer         *buffer.Buffer

	// Multi-line tracking for stack traces
	inTraceback    bool
	tracebackLines []string
	lastErrorEvent *buffer.Event
}

// New creates a new Tailer
func New(path, format, organizationID, serviceName, environment string, globalTags map[string]string, buf *buffer.Buffer) *Tailer {
	return &Tailer{
		path:           path,
		format:         format,
		organizationID: organizationID,
		serviceName:    serviceName,
		environment:    environment,
		globalTags:     globalTags,
		buffer:         buf,
	}
}

// Start starts tailing the log file
func (t *Tailer) Start() error {
	// Configure tail
	config := tail.Config{
		Follow: true, // Continue watching for new lines
		ReOpen: true, // Reopen file if rotated
		Poll:   true, // Use polling (works with log rotation)
		Location: &tail.SeekInfo{
			Offset: 0,
			Whence: 2, // Start at end of file (only read new lines)
		},
	}

	// Start tailing
	tailFile, err := tail.TailFile(t.path, config)
	if err != nil {
		return err
	}

	log.Printf("[Tailer] Started tailing %s (format: %s)", t.path, t.format)

	// Read lines
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[Tailer] Panic recovered in %s: %v", t.path, r)
			}
		}()

		for line := range tailFile.Lines {
			if line.Err != nil {
				log.Printf("[Tailer] Error reading %s: %v", t.path, line.Err)
				continue
			}

			// Handle multi-line tracebacks for Django format
			if t.format == "django" {
				if t.handleMultiLineLog(line.Text) {
					continue // Line was part of traceback
				}
			}

			// Parse log line
			event := ParseLog(line.Text, t.format, t.organizationID, t.serviceName, t.environment)
			if event == nil {
				continue
			}

			if !scrubber.Apply(*event) {
				continue
			}

			// Merge global tags with event-specific tags
			if len(t.globalTags) > 0 {
				eventTags, ok := (*event)["tags"].(map[string]string)
				if !ok || eventTags == nil {
					// No existing tags, use global tags
					(*event)["tags"] = t.globalTags
				} else {
					// Merge tags (event-specific tags take priority)
					for k, v := range t.globalTags {
						if _, exists := eventTags[k]; !exists {
							eventTags[k] = v
						}
					}
				}
			}

			// Track error events for potential tracebacks
			if t.format == "django" {
				if level, ok := (*event)["level"].(string); ok && (level == "error" || level == "critical") {
					t.lastErrorEvent = event
				}
			}

			// Add to buffer
			t.buffer.Add(*event)
		}
	}()

	return nil
}

// handleMultiLineLog processes multi-line log entries (like stack traces)
// Returns true if the line was handled as part of a multi-line log
func (t *Tailer) handleMultiLineLog(line string) bool {
	// Check if this is the start of a traceback
	if !t.inTraceback && line == "Traceback (most recent call last):" {
		t.inTraceback = true
		t.tracebackLines = []string{line}
		return true
	}

	// If we're in a traceback, accumulate lines
	if t.inTraceback {
		t.tracebackLines = append(t.tracebackLines, line)

		// Check if this is the end of the traceback
		// Tracebacks typically end with an exception line (not indented as much)
		// e.g., "ValueError: Something went wrong"
		// Look for lines that don't start with lots of spaces and contain ": "
		trimmed := strings.TrimSpace(line)
		if len(trimmed) > 0 && !strings.HasPrefix(line, "  File ") && !strings.HasPrefix(line, "    ") {
			// Check if it looks like an exception (contains ": " or is an exception class)
			if strings.Contains(trimmed, ": ") || isExceptionLine(trimmed) {
				// Traceback complete - attach to last error event
				if t.lastErrorEvent != nil {
					stacktrace := strings.Join(t.tracebackLines, "\n")
					(*t.lastErrorEvent)["stacktrace"] = stacktrace
					log.Printf("[Tailer] Captured traceback (%d lines) for error event", len(t.tracebackLines))
				}

				// Reset state
				t.inTraceback = false
				t.tracebackLines = nil
				t.lastErrorEvent = nil
				return true
			}
		}

		return true
	}

	return false
}

// isExceptionLine checks if a line looks like a Python exception
func isExceptionLine(line string) bool {
	// Common Python exception types
	exceptions := []string{
		"Exception", "Error", "Warning",
		"ValueError", "TypeError", "KeyError", "AttributeError",
		"IndexError", "RuntimeError", "ImportError", "IOError",
		"OSError", "NameError", "SyntaxError", "IndentationError",
		"AssertionError", "ZeroDivisionError", "FileNotFoundError",
	}

	for _, exc := range exceptions {
		if strings.HasPrefix(line, exc) {
			return true
		}
	}

	return false
}
