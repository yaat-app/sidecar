package scrubber

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/yaat-app/sidecar/internal/buffer"
	"github.com/yaat-app/sidecar/internal/config"
)

type fieldKind int

const (
	fieldTopLevel fieldKind = iota
	fieldTagExact
	fieldTagWildcard
)

type fieldSelector struct {
	kind fieldKind
	key  string
}

type compiledRule struct {
	name        string
	pattern     *regexp.Regexp
	replacement string
	fields      []fieldSelector
	drop        bool
}

var (
	mu          sync.RWMutex
	activeRules []*compiledRule
	enabled     bool
)

// Configure installs scrubbing rules compiled from configuration.
func Configure(cfg config.ScrubbingConfig) error {
	mu.Lock()
	defer mu.Unlock()

	if !cfg.Enabled || len(cfg.Rules) == 0 {
		activeRules = nil
		enabled = false
		return nil
	}

	compiled := make([]*compiledRule, 0, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		pattern := strings.TrimSpace(rule.Pattern)
		if pattern == "" {
			return fmt.Errorf("scrubbing rule %q has an empty pattern", rule.Name)
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("scrubbing rule %q: %w", rule.Name, err)
		}
		selectors := buildSelectors(rule.Fields)
		compiled = append(compiled, &compiledRule{
			name:        rule.Name,
			pattern:     re,
			replacement: rule.Replacement,
			fields:      selectors,
			drop:        rule.Drop,
		})
	}

	activeRules = compiled
	enabled = true
	return nil
}

// Apply applies configured rules to the provided event. Returns false when the
// event should be dropped.
func Apply(evt buffer.Event) bool {
	if evt == nil {
		return true
	}

	mu.RLock()
	rules := activeRules
	active := enabled
	mu.RUnlock()

	if !active || len(rules) == 0 {
		return true
	}

	for _, rule := range rules {
		if !rule.apply(evt) {
			return false
		}
	}
	return true
}

func (r *compiledRule) apply(evt buffer.Event) bool {
	for _, selector := range r.fields {
		switch selector.kind {
		case fieldTopLevel:
			value, ok := getStringField(evt, selector.key)
			if !ok {
				continue
			}
			if r.drop {
				if r.pattern.MatchString(value) {
					return false
				}
				continue
			}
			replaced := r.pattern.ReplaceAllString(value, r.replacement)
			if replaced != value {
				evt[selector.key] = replaced
			}
		case fieldTagExact:
			tags := ensureTags(evt)
			if tags == nil {
				continue
			}
			value, ok := tags[selector.key]
			if !ok {
				continue
			}
			if r.drop {
				if r.pattern.MatchString(value) {
					return false
				}
				continue
			}
			replaced := r.pattern.ReplaceAllString(value, r.replacement)
			if replaced != value {
				tags[selector.key] = replaced
			}
		case fieldTagWildcard:
			tags := ensureTags(evt)
			if tags == nil {
				continue
			}
			for key, value := range tags {
				if r.drop {
					if r.pattern.MatchString(value) {
						return false
					}
					continue
				}
				replaced := r.pattern.ReplaceAllString(value, r.replacement)
				if replaced != value {
					tags[key] = replaced
				}
			}
		}
	}
	return true
}

func buildSelectors(fields []string) []fieldSelector {
	if len(fields) == 0 {
		return []fieldSelector{
			{kind: fieldTopLevel, key: "message"},
			{kind: fieldTopLevel, key: "stacktrace"},
		}
	}

	selectors := make([]fieldSelector, 0, len(fields))
	for _, f := range fields {
		field := strings.TrimSpace(f)
		if field == "" {
			continue
		}
		lower := strings.ToLower(field)
		if strings.HasPrefix(lower, "tags.") {
			key := strings.TrimSpace(field[5:])
			if key == "" || key == "*" {
				selectors = append(selectors, fieldSelector{kind: fieldTagWildcard})
			} else {
				selectors = append(selectors, fieldSelector{kind: fieldTagExact, key: key})
			}
			continue
		}
		selectors = append(selectors, fieldSelector{kind: fieldTopLevel, key: field})
	}

	if len(selectors) == 0 {
		return []fieldSelector{
			{kind: fieldTopLevel, key: "message"},
			{kind: fieldTopLevel, key: "stacktrace"},
		}
	}

	return selectors
}

func getStringField(evt buffer.Event, key string) (string, bool) {
	value, ok := evt[key]
	if !ok {
		return "", false
	}
	str, ok := value.(string)
	if !ok {
		return "", false
	}
	return str, true
}

func ensureTags(evt buffer.Event) map[string]string {
	raw, ok := evt["tags"]
	if !ok {
		return nil
	}

	switch tags := raw.(type) {
	case map[string]string:
		return tags
	case map[string]interface{}:
		converted := make(map[string]string, len(tags))
		for k, v := range tags {
			converted[k] = fmt.Sprint(v)
		}
		evt["tags"] = converted
		return converted
	default:
		return nil
	}
}
