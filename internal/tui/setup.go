package tui

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yaat-app/sidecar/internal/config"
	"github.com/yaat-app/sidecar/internal/detection"
	"github.com/yaat-app/sidecar/internal/state"
)

type setupStep int

const (
	stepAPIKey setupStep = iota
	stepServiceName
	stepEnvironment
	stepLogFiles
	stepScrubbing
	stepReview
	stepDone
)

type SetupWizard struct {
	step        setupStep
	apiKey      textinput.Model
	serviceName textinput.Model
	environment int // 0=production, 1=staging, 2=development

	// Log file selection
	detectedLogs []detection.LogFile
	selectedLogs map[int]bool
	logCursor    int

	// Scrubbing selection
	scrubOptions  []config.ScrubRule
	scrubSelected map[int]bool
	scrubCursor   int

	// State
	err        error
	saved      bool
	configPath string
}

func NewSetupWizard() *SetupWizard {
	// Create text inputs
	apiKey := textinput.New()
	apiKey.Placeholder = "yaat_..."
	apiKey.Focus()
	apiKey.Width = 50
	apiKey.EchoMode = textinput.EchoPassword
	apiKey.EchoCharacter = '*'

	serviceName := textinput.New()
	serviceName.Placeholder = "my-service"
	serviceName.Width = 50

	// Auto-detect hostname as default service name
	if hostname, err := os.Hostname(); err == nil {
		serviceName.SetValue(hostname)
	}

	// Auto-detect log files
	env := detection.DetectEnvironment()
	recommended := config.RecommendedScrubRules()
	selectedScrub := make(map[int]bool, len(recommended))
	for idx := range recommended {
		selectedScrub[idx] = true
	}

	selectedLogs := make(map[int]bool, len(env.LogFiles))
	for idx := range env.LogFiles {
		if env.LogFiles[idx].Readable {
			selectedLogs[idx] = true
		}
	}

	return &SetupWizard{
		step:          stepAPIKey,
		apiKey:        apiKey,
		serviceName:   serviceName,
		environment:   0, // Default to production
		detectedLogs:  env.LogFiles,
		selectedLogs:  selectedLogs,
		scrubOptions:  recommended,
		scrubSelected: selectedScrub,
		configPath:    os.ExpandEnv("$HOME/.yaat/yaat.yaml"),
	}
}

func (s *SetupWizard) Update(msg tea.Msg) (*SetupWizard, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			// Cancel setup
			return s, nil

		case "enter":
			return s.nextStep()

		case "up":
			if s.step == stepEnvironment {
				if s.environment > 0 {
					s.environment--
				}
			} else if s.step == stepLogFiles {
				if s.logCursor > 0 {
					s.logCursor--
				}
			} else if s.step == stepScrubbing {
				if s.scrubCursor > 0 {
					s.scrubCursor--
				}
			}

		case "down":
			if s.step == stepEnvironment {
				if s.environment < 2 {
					s.environment++
				}
			} else if s.step == stepLogFiles {
				if s.logCursor < len(s.detectedLogs)-1 {
					s.logCursor++
				}
			} else if s.step == stepScrubbing {
				if s.scrubCursor < len(s.scrubOptions)-1 {
					s.scrubCursor++
				}
			}

		case " ": // Space to toggle log file selection
			if s.step == stepLogFiles {
				s.selectedLogs[s.logCursor] = !s.selectedLogs[s.logCursor]
			} else if s.step == stepScrubbing {
				s.scrubSelected[s.scrubCursor] = !s.scrubSelected[s.scrubCursor]
			}
		}
	}

	// Update text inputs
	var cmd tea.Cmd
	if s.step == stepAPIKey {
		s.apiKey, cmd = s.apiKey.Update(msg)
		return s, cmd
	} else if s.step == stepServiceName {
		s.serviceName, cmd = s.serviceName.Update(msg)
		return s, cmd
	}

	return s, nil
}

func (s *SetupWizard) nextStep() (*SetupWizard, tea.Cmd) {
	switch s.step {
	case stepAPIKey:
		if s.apiKey.Value() == "" {
			s.err = fmt.Errorf("API key is required")
			return s, nil
		}
		s.err = nil
		s.step = stepServiceName
		s.serviceName.Focus()

	case stepServiceName:
		if s.serviceName.Value() == "" {
			s.err = fmt.Errorf("Service name is required")
			return s, nil
		}
		s.err = nil
		s.step = stepEnvironment

	case stepEnvironment:
		s.err = nil
		if len(s.detectedLogs) > 0 {
			s.step = stepLogFiles
		} else {
			s.step = stepScrubbing
		}

	case stepLogFiles:
		s.err = nil
		s.step = stepScrubbing

	case stepScrubbing:
		s.err = nil
		s.step = stepReview

	case stepReview:
		// Save configuration
		if err := s.saveConfig(); err != nil {
			s.err = err
			return s, nil
		}
		s.saved = true
		s.step = stepDone
	}

	return s, nil
}

func (s *SetupWizard) saveConfig() error {
	// Build log file list
	var logs []config.LogConfig
	for i, log := range s.detectedLogs {
		if s.selectedLogs[i] {
			logs = append(logs, config.LogConfig{
				Path:   log.Path,
				Format: log.SuggestedFormat,
			})
		}
	}

	var rules []config.ScrubRule
	for i, rule := range s.scrubOptions {
		if s.scrubSelected[i] {
			rules = append(rules, rule)
		}
	}

	// Build config
	envName := []string{"production", "staging", "development"}[s.environment]

	cfg := &config.Config{
		APIKey:        s.apiKey.Value(),
		ServiceName:   s.serviceName.Value(),
		Environment:   envName,
		APIEndpoint:   "https://yaat.io/api/v1/ingest",
		BufferSize:    1000,
		FlushInterval: "10s",
		Logs:          logs,
		Scrubbing: config.ScrubbingConfig{
			Enabled: len(rules) > 0,
			Rules:   rules,
		},
		Proxy: config.ProxyConfig{
			Enabled: false,
		},
	}

	// Create directory if needed
	dir := filepath.Dir(s.configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Save config
	if err := config.SaveConfig(s.configPath, cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	if err := state.RecordConfig(cfg.SourcePath); err != nil {
		return fmt.Errorf("failed to update local state: %w", err)
	}

	return nil
}

func (s *SetupWizard) View() string {
	var content string

	header := TitleStyle.Render("Setup Wizard") + "\n\n"

	switch s.step {
	case stepAPIKey:
		content = s.renderAPIKeyStep()
	case stepServiceName:
		content = s.renderServiceNameStep()
	case stepEnvironment:
		content = s.renderEnvironmentStep()
	case stepLogFiles:
		content = s.renderLogFilesStep()
	case stepScrubbing:
		content = s.renderScrubbingStep()
	case stepReview:
		content = s.renderReviewStep()
	case stepDone:
		content = s.renderDoneStep()
	}

	// Error message
	if s.err != nil {
		content += "\n" + ErrorStyle.Render(fmt.Sprintf("Error: %v", s.err)) + "\n"
	}

	// Help text
	help := "\n" + MutedStyle.Render("[Enter] Continue  [Esc] Cancel")

	return BaseStyle.Render(header+content+help) + "\n"
}

func (s *SetupWizard) renderAPIKeyStep() string {
	title := SectionHeaderStyle.Render("Step 1 of 6: API Key") + "\n\n"
	desc := MutedStyle.Render("Enter your YAAT API key") + "\n"
	desc += MutedStyle.Render("Get it from: https://yaat.io/settings") + "\n\n"

	return title + desc + s.apiKey.View() + "\n"
}

func (s *SetupWizard) renderServiceNameStep() string {
	title := SectionHeaderStyle.Render("Step 2 of 6: Service Name") + "\n\n"
	desc := MutedStyle.Render("Name for this service") + "\n\n"

	return title + desc + s.serviceName.View() + "\n"
}

func (s *SetupWizard) renderEnvironmentStep() string {
	title := SectionHeaderStyle.Render("Step 3 of 6: Environment") + "\n\n"
	desc := MutedStyle.Render("Select environment type") + "\n\n"

	options := []string{"Production", "Staging", "Development"}

	var optionsText string
	for i, opt := range options {
		if i == s.environment {
			optionsText += SuccessStyle.Render("▸ "+opt) + "\n"
		} else {
			optionsText += MutedStyle.Render("  "+opt) + "\n"
		}
	}

	help := MutedStyle.Render("\n[↑↓] Navigate  [Enter] Select")

	return title + desc + optionsText + help + "\n"
}

func (s *SetupWizard) renderLogFilesStep() string {
	title := SectionHeaderStyle.Render("Step 4 of 6: Log Files") + "\n\n"
	desc := MutedStyle.Render(fmt.Sprintf("Found %d log files - select which to monitor", len(s.detectedLogs))) + "\n"
	if len(s.detectedLogs) > 0 {
		desc += MutedStyle.Render("Readable sources are pre-selected for you. Press space to adjust.") + "\n\n"
	} else {
		desc += "\n"
	}

	if len(s.detectedLogs) == 0 {
		desc += WarningStyle.Render("No log files detected") + "\n"
		desc += MutedStyle.Render("You can configure them manually later") + "\n"
	} else {
		for i, log := range s.detectedLogs {
			checkbox := "[ ]"
			if s.selectedLogs[i] {
				checkbox = "[✓]"
			}

			cursor := "  "
			if i == s.logCursor {
				cursor = "▸ "
			}

			format := MutedStyle.Render(fmt.Sprintf("(%s)", log.SuggestedFormat))
			readable := ""
			if !log.Readable {
				readable = ErrorStyle.Render(" [not readable]")
			}

			source := ""
			if log.Source != "" {
				source = MutedStyle.Render(" - " + log.Source)
			}

			line := cursor + checkbox + " " + ValueStyle.Render(log.Path) + " " + format + source + readable + "\n"
			desc += line
		}
	}

	help := MutedStyle.Render("\n[↑↓] Navigate  [Space] Toggle  [Enter] Continue")

	return title + desc + help + "\n"
}

func (s *SetupWizard) renderScrubbingStep() string {
	title := SectionHeaderStyle.Render("Step 5 of 6: Data Scrubbing") + "\n\n"
	desc := MutedStyle.Render("Select scrubbing rules to prevent sensitive data from leaving this machine") + "\n\n"

	if len(s.scrubOptions) == 0 {
		desc += WarningStyle.Render("No scrubbing rules available") + "\n"
		desc += MutedStyle.Render("You can edit yaat.yaml later to add custom rules") + "\n"
	} else {
		for i, rule := range s.scrubOptions {
			checkbox := "[ ]"
			if s.scrubSelected[i] {
				checkbox = "[✓]"
			}

			cursor := "  "
			if i == s.scrubCursor {
				cursor = "▸ "
			}

			line := cursor + checkbox + " " + ValueStyle.Render(rule.Name) + "\n"
			desc += line
			if rule.Description != "" {
				desc += "     " + MutedStyle.Render(rule.Description) + "\n"
			}
		}
	}

	help := MutedStyle.Render("\n[↑↓] Navigate  [Space] Toggle  [Enter] Continue")
	return title + desc + help + "\n"
}

func (s *SetupWizard) renderReviewStep() string {
	title := SectionHeaderStyle.Render("Step 6 of 6: Review") + "\n\n"

	content := LabelStyle.Render("Service Name:  ") + ValueStyle.Render(s.serviceName.Value()) + "\n"
	content += LabelStyle.Render("Environment:   ") + ValueStyle.Render([]string{"production", "staging", "development"}[s.environment]) + "\n"
	content += LabelStyle.Render("API Key:       ") + ValueStyle.Render(maskKey(s.apiKey.Value())) + "\n"

	selectedCount := 0
	for _, selected := range s.selectedLogs {
		if selected {
			selectedCount++
		}
	}
	content += LabelStyle.Render("Log Files:     ") + ValueStyle.Render(fmt.Sprintf("%d selected", selectedCount)) + "\n"

	scrubCount := 0
	for _, selected := range s.scrubSelected {
		if selected {
			scrubCount++
		}
	}
	if scrubCount > 0 {
		content += LabelStyle.Render("Scrubbing:     ") + ValueStyle.Render(fmt.Sprintf("%d rule(s) enabled", scrubCount)) + "\n"
	} else {
		content += LabelStyle.Render("Scrubbing:     ") + WarningStyle.Render("disabled") + "\n"
	}

	content += "\n" + MutedStyle.Render("Configuration will be saved to:") + "\n"
	content += MutedStyle.Render(s.configPath) + "\n"

	return title + content + "\n"
}

func (s *SetupWizard) renderDoneStep() string {
	title := SuccessStyle.Render("✓ Setup Complete!") + "\n\n"

	content := MutedStyle.Render("Configuration saved to:") + "\n"
	content += ValueStyle.Render(s.configPath) + "\n\n"

	content += SuccessStyle.Render("Next steps:") + "\n"
	content += MutedStyle.Render("  • Press any key to return to dashboard") + "\n"
	content += MutedStyle.Render("  • Run 'yaat-sidecar --start' to start the daemon") + "\n"

	return title + content + "\n"
}

func maskKey(key string) string {
	if len(key) < 10 {
		return "***"
	}
	return key[:7] + "***************"
}

func (s *SetupWizard) IsDone() bool {
	return s.step == stepDone && s.saved
}
