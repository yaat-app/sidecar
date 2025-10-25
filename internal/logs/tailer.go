package logs

import (
	"log"

	"github.com/hpcloud/tail"
	"github.com/yaat/sidecar/internal/buffer"
)

// Tailer tails a log file and parses lines
type Tailer struct {
	path        string
	format      string
	serviceName string
	environment string
	buffer      *buffer.Buffer
}

// New creates a new Tailer
func New(path, format, serviceName, environment string, buf *buffer.Buffer) *Tailer {
	return &Tailer{
		path:        path,
		format:      format,
		serviceName: serviceName,
		environment: environment,
		buffer:      buf,
	}
}

// Start starts tailing the log file
func (t *Tailer) Start() error {
	// Configure tail
	config := tail.Config{
		Follow: true,               // Continue watching for new lines
		ReOpen: true,               // Reopen file if rotated
		Poll:   true,               // Use polling (works with log rotation)
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

			// Parse log line
			event := ParseLog(line.Text, t.format, t.serviceName, t.environment)
			if event == nil {
				continue
			}

			// Add to buffer
			t.buffer.Add(*event)
		}
	}()

	return nil
}
