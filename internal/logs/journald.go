//go:build linux && cgo
// +build linux,cgo

package logs

import (
	"context"
	"fmt"
	"time"

	"github.com/coreos/go-systemd/v22/sdjournal"

	"github.com/yaat-app/sidecar/internal/buffer"
	"github.com/yaat-app/sidecar/internal/scrubber"
)

// JournaldTailer reads entries from systemd-journald and converts them to events.
type JournaldTailer struct {
	serviceName string
	environment string
	buf         *buffer.Buffer
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewJournaldTailer creates a journald tailer.
func NewJournaldTailer(serviceName, environment string, buf *buffer.Buffer) *JournaldTailer {
	ctx, cancel := context.WithCancel(context.Background())
	return &JournaldTailer{
		serviceName: serviceName,
		environment: environment,
		buf:         buf,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start begins tailing. It spawns a goroutine; callers should maintain lifecycle via returned cancel func.
func (t *JournaldTailer) Start(matchUnit string) error {
	journal, err := sdjournal.NewJournal()
	if err != nil {
		return fmt.Errorf("open journald: %w", err)
	}

	if matchUnit != "" {
		if err := journal.AddMatch(fmt.Sprintf("_SYSTEMD_UNIT=%s", matchUnit)); err != nil {
			_ = journal.Close()
			return fmt.Errorf("journald add match: %w", err)
		}
	}

	if err := journal.SeekTail(); err != nil {
		_ = journal.Close()
		return fmt.Errorf("journald seek tail: %w", err)
	}
	// Skip existing entries
	_, _ = journal.Next()

	go func() {
		defer journal.Close()

		for {
			select {
			case <-t.ctx.Done():
				return
			default:
			}

			if ret := journal.Next(); ret < 0 {
				if ret == -int(sdjournal.EOF) {
					time.Sleep(200 * time.Millisecond)
					continue
				}
				time.Sleep(200 * time.Millisecond)
				continue
			}

			entry, err := journal.GetEntry()
			if err != nil {
				time.Sleep(200 * time.Millisecond)
				continue
			}

			event := t.convertEntry(entry)
			if scrubber.Apply(event) {
				t.buf.Add(event)
			}
		}
	}()

	return nil
}

// Stop cancels the tailer.
func (t *JournaldTailer) Stop() {
	t.cancel()
}

func (t *JournaldTailer) convertEntry(entry *sdjournal.JournalEntry) buffer.Event {
	timestamp := time.Unix(0, int64(entry.RealtimeTimestamp)*int64(time.Microsecond)).UTC()
	message := entry.Fields["MESSAGE"]
	priority := entry.Fields["PRIORITY"]
	unit := entry.Fields["_SYSTEMD_UNIT"]
	identifier := entry.Fields["SYSLOG_IDENTIFIER"]

	level := mapJournalPriority(priority)
	tags := map[string]string{
		"journal.unit":       unit,
		"journal.priority":   priority,
		"journal.identifier": identifier,
		"journal.hostname":   entry.Fields["_HOSTNAME"],
		"journal.transport":  entry.Fields["__TRANSPORT"],
		"journal.pid":        entry.Fields["_PID"],
		"journal.comm":       entry.Fields["_COMM"],
		"journal.executable": entry.Fields["_EXE"],
		"journal.subsystem":  entry.Fields["SYSLOG_FACILITY"],
	}
	for k, v := range tags {
		if v == "" {
			delete(tags, k)
		}
	}

	return buffer.Event{
		"service_name": t.serviceName,
		"environment":  t.environment,
		"event_type":   "log",
		"timestamp":    timestamp.Format(time.RFC3339Nano),
		"level":        level,
		"message":      message,
		"tags":         tags,
	}
}

func mapJournalPriority(priority string) string {
	switch priority {
	case "0", "1":
		return "critical"
	case "2", "3":
		return "error"
	case "4":
		return "warning"
	case "5", "6":
		return "info"
	case "7":
		return "debug"
	default:
		return "info"
	}
}
