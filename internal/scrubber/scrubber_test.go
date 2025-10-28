package scrubber

import (
	"testing"

	"github.com/yaat-app/sidecar/internal/buffer"
	"github.com/yaat-app/sidecar/internal/config"
)

func TestScrubberMasksMessage(t *testing.T) {
	cfg := config.ScrubbingConfig{
		Enabled: true,
		Rules: []config.ScrubRule{
			{
				Name:        "Mask Emails",
				Pattern:     `(?i)[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}`,
				Replacement: "[EMAIL]",
				Fields:      []string{"message"},
			},
		},
	}

	if err := Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}
	defer Configure(config.ScrubbingConfig{})

	event := buffer.Event{
		"message": "user email john.doe@example.com logged in",
	}

	if !Apply(event) {
		t.Fatal("expected event to be kept")
	}

	if got := event["message"]; got != "user email [EMAIL] logged in" {
		t.Fatalf("unexpected message value: %v", got)
	}
}

func TestScrubberDropsOnMatch(t *testing.T) {
	cfg := config.ScrubbingConfig{
		Enabled: true,
		Rules: []config.ScrubRule{
			{
				Name:    "Drop health checks",
				Pattern: `^/healthz$`,
				Fields:  []string{"tags.path"},
				Drop:    true,
			},
		},
	}

	if err := Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}
	defer Configure(config.ScrubbingConfig{})

	event := buffer.Event{
		"message": "request handled",
		"tags": map[string]string{
			"path": "/healthz",
		},
	}

	if Apply(event) {
		t.Fatal("expected event to be dropped")
	}
}

func TestScrubberWildcardTags(t *testing.T) {
	cfg := config.ScrubbingConfig{
		Enabled: true,
		Rules: []config.ScrubRule{
			{
				Name:        "Mask tokens",
				Pattern:     `(?i)token=[A-Za-z0-9]+`,
				Replacement: "token=[REDACTED]",
				Fields:      []string{"tags.*"},
			},
		},
	}

	if err := Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}
	defer Configure(config.ScrubbingConfig{})

	event := buffer.Event{
		"tags": map[string]string{
			"query": "user=1&token=secret",
		},
	}

	if !Apply(event) {
		t.Fatal("expected event kept")
	}

	tags := event["tags"].(map[string]string)
	if tags["query"] != "user=1&token=[REDACTED]" {
		t.Fatalf("unexpected tag value: %v", tags["query"])
	}
}

func TestScrubberIgnoresWhenDisabled(t *testing.T) {
	if err := Configure(config.ScrubbingConfig{}); err != nil {
		t.Fatalf("configure: %v", err)
	}

	event := buffer.Event{
		"message": "no changes",
	}

	if !Apply(event) {
		t.Fatal("expected event kept")
	}
}
