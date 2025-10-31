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
	organizationID string
	serviceName    string
	environment    string
	globalTags     map[string]string
	buf            *buffer.Buffer
	ctx            context.Context
	cancel         context.CancelFunc
}

// NewJournaldTailer creates a journald tailer.
func NewJournaldTailer(organizationID, serviceName, environment string, globalTags map[string]string, buf *buffer.Buffer) *JournaldTailer {
	ctx, cancel := context.WithCancel(context.Background())
	return &JournaldTailer{
		organizationID: organizationID,
		serviceName:    serviceName,
		environment:    environment,
		globalTags:     globalTags,
		buf:            buf,
		ctx:            ctx,
		cancel:         cancel,
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

			n, err := journal.Next()
			if err != nil {
				// Error occurred, wait and retry
				time.Sleep(200 * time.Millisecond)
				continue
			}

			if n == 0 {
				// No new entries, wait before checking again
				time.Sleep(200 * time.Millisecond)
				continue
			}

			entry, err := journal.GetEntry()
			if err != nil {
				time.Sleep(200 * time.Millisecond)
				continue
			}

			event := t.convertEntry(entry)

			// Merge global tags with event-specific tags
			if len(t.globalTags) > 0 {
				eventTags, ok := event["tags"].(map[string]string)
				if !ok || eventTags == nil {
					// No existing tags, use global tags
					event["tags"] = t.globalTags
				} else {
					// Merge tags (event-specific tags take priority)
					for k, v := range t.globalTags {
						if _, exists := eventTags[k]; !exists {
							eventTags[k] = v
						}
					}
				}
			}

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
		"organization_id": t.organizationID,
		"service_name":    t.serviceName,
		"environment":     t.environment,
		"event_type":      "log",
		"timestamp":       timestamp.Format(time.RFC3339Nano),
		"level":           level,
		"message":         message,
		"tags":            tags,
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
