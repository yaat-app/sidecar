package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yaat-app/sidecar/internal/buffer"
	"github.com/yaat-app/sidecar/internal/config"
	"github.com/yaat-app/sidecar/internal/daemon"
	"github.com/yaat-app/sidecar/internal/diag"
	"github.com/yaat-app/sidecar/internal/forwarder"
	"github.com/yaat-app/sidecar/internal/state"
)

// View types
type viewType int

const (
	viewDashboard viewType = iota
	viewConfig
	viewConfigEdit
	viewEvents
	viewTest
	viewSetup
	viewUninstall
)

// Dashboard model
type Dashboard struct {
	width  int
	height int

	// Current view
	currentView viewType
	message     string

	// Real configuration
	config      *config.Config
	configPath  string
	configError error

	// Service status
	isRunning bool
	uptime    time.Duration
	startTime time.Time

	// Log files from config
	tailedFiles []TailedFile

	// Test results
	testResults  []TestResult
	lastTest     state.TestResult
	stateError   error
	diagSnapshot diag.Snapshot

	// Setup wizard
	setupWizard *SetupWizard

	// Config editor
	configEditor *ConfigEditor

	// Uninstall state
	uninstallConfirm bool
	uninstallResult  string
	uninstallWarnings []string

	// Quit flag
	quitting bool
}

type TailedFile struct {
	Path     string
	Exists   bool
	Readable bool
}

type TestResult struct {
	Name   string
	Status string
	Detail string
}

// NewDashboard creates a new dashboard
func NewDashboard() *Dashboard {
	// Try to load actual config
	cfg, cfgPath, err := loadConfig()

	// Determine log files from config
	var logFiles []TailedFile
	if cfg != nil {
		for _, log := range cfg.Logs {
			exists := false
			readable := false

			if info, statErr := os.Stat(log.Path); statErr == nil {
				exists = true
				readable = !info.IsDir()
			}

			logFiles = append(logFiles, TailedFile{
				Path:     log.Path,
				Exists:   exists,
				Readable: readable,
			})
		}
	}

	dashboard := &Dashboard{
		currentView: viewDashboard,
		config:      cfg,
		configPath:  cfgPath,
		configError: err,
		tailedFiles: logFiles,
	}

	if st, stateErr := state.Load(); stateErr != nil {
		dashboard.stateError = stateErr
	} else if st != nil {
		dashboard.lastTest = st.LastTest
		if dashboard.config == nil && st.ConfigPath != "" {
			dashboard.configPath = st.ConfigPath
		}
	}

	dashboard.diagSnapshot = diag.Global().Snapshot()

	return dashboard
}

// loadConfig tries to find and load configuration
func loadConfig() (*config.Config, string, error) {
	// Try standard locations
	paths := []string{
		"yaat.yaml",
		os.ExpandEnv("$HOME/.yaat/yaat.yaml"),
		os.ExpandEnv("$HOME/.config/yaat/yaat.yaml"),
		"/etc/yaat/yaat.yaml",
	}

	for _, path := range paths {
		if cfg, err := config.LoadConfig(path); err == nil {
			return cfg, path, nil
		}
	}

	return nil, "", fmt.Errorf("no configuration file found")
}

// Init initializes the dashboard
func (m Dashboard) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
	)
}

// Update handles messages
func (m Dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}

		if m.currentView == viewConfigEdit && m.configEditor != nil {
			cmd := m.configEditor.Update(msg)
			m.handleConfigEditorResult()
			return m, cmd
		}

		switch msg.String() {
		case "s":
			if m.currentView != viewSetup {
				m.currentView = viewSetup
				m.setupWizard = NewSetupWizard()
			}
			return m, nil

		case "c":
			if m.currentView == viewConfig {
				m.currentView = viewDashboard
				m.message = ""
				m.configEditor = nil
			} else {
				m.currentView = viewConfig
				m.configEditor = nil
			}
			return m, nil

		case "e":
			if m.currentView == viewEvents {
				m.currentView = viewDashboard
				m.message = ""
			} else {
				m.currentView = viewEvents
			}
			return m, nil

		case "t":
			if m.currentView == viewTest {
				m.currentView = viewDashboard
				m.message = ""
			} else {
				m.currentView = viewTest
				m.runTests()
			}
			return m, nil

		case "u":
			if m.currentView == viewUninstall {
				m.currentView = viewDashboard
				m.message = ""
				m.uninstallConfirm = false
				m.uninstallResult = ""
				m.uninstallWarnings = nil
			} else {
				m.currentView = viewUninstall
				m.uninstallConfirm = false
				m.uninstallResult = ""
				m.uninstallWarnings = nil
			}
			return m, nil

		case "y", "Y":
			if m.currentView == viewUninstall && !m.uninstallConfirm {
				m.uninstallConfirm = true
				warnings, err := daemon.Uninstall()
				if err != nil {
					m.uninstallWarnings = append(warnings, err.Error())
					m.uninstallResult = "error"
				} else {
					m.uninstallWarnings = warnings
					if len(warnings) == 0 {
						m.uninstallResult = "success"
					} else {
						m.uninstallResult = "partial"
					}
				}
			}
			return m, nil

		case "n", "N":
			if m.currentView == viewUninstall {
				m.currentView = viewDashboard
				m.message = ""
				m.uninstallConfirm = false
				m.uninstallResult = ""
				m.uninstallWarnings = nil
			}
			return m, nil

		case "enter":
			if m.currentView == viewConfig {
				m.startConfigEditor()
				return m, nil
			}
		}

		if m.currentView == viewSetup && m.setupWizard != nil {
			wizard, cmd := m.setupWizard.Update(msg)
			m.setupWizard = wizard
			m.handleSetupWizardCompletion()
			return m, cmd
		}

		if m.currentView != viewDashboard && m.currentView != viewSetup {
			m.currentView = viewDashboard
			m.message = ""
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		// Update daemon status
		defaultPidPath := "/var/run/yaat-sidecar.pid"
		m.isRunning = daemon.IsRunning(defaultPidPath)
		if m.isRunning {
			m.uptime += 1 * time.Second
		}
		m.diagSnapshot = diag.Global().Snapshot()
		if m.currentView == viewConfigEdit && m.configEditor != nil {
			cmd := m.configEditor.Update(msg)
			m.handleConfigEditorResult()
			return m, tea.Batch(cmd, tickCmd())
		}
		return m, tickCmd()
	default:
		if m.currentView == viewConfigEdit && m.configEditor != nil {
			cmd := m.configEditor.Update(msg)
			m.handleConfigEditorResult()
			return m, cmd
		}
	}

	return m, nil
}

func (m *Dashboard) startConfigEditor() {
	path := m.configPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	m.configEditor = NewConfigEditor(m.config, path)
	m.currentView = viewConfigEdit
	m.message = ""
}

func (m *Dashboard) handleConfigEditorResult() {
	if m.configEditor == nil || !m.configEditor.Done() {
		return
	}

	editor := m.configEditor
	m.configEditor = nil

	if editor.Saved() {
		path := editor.SavedPath()
		cfg, err := config.LoadConfig(path)
		if err != nil {
			m.configError = err
			m.message = ""
			m.currentView = viewConfig
			return
		}
		m.config = cfg
		m.configPath = path
		m.configError = nil
		m.rebuildLogFiles()
		if err := state.RecordConfig(path); err != nil {
			m.message = fmt.Sprintf("Configuration saved to %s (state update failed: %v)", path, err)
		} else {
			m.message = fmt.Sprintf("Configuration saved to %s", path)
		}
		m.currentView = viewConfig
		return
	}

	if editor.Cancelled() {
		m.message = ""
		m.currentView = viewConfig
	}
}

func (m *Dashboard) handleSetupWizardCompletion() {
	if m.setupWizard == nil || !m.setupWizard.IsDone() {
		return
	}

	cfg, cfgPath, err := loadConfig()
	m.config = cfg
	m.configPath = cfgPath
	m.configError = err
	m.rebuildLogFiles()
	if cfgPath != "" {
		_ = state.RecordConfig(cfgPath)
	}
	m.currentView = viewDashboard
	m.message = "Configuration saved successfully!"
	m.setupWizard = nil
}

func (m *Dashboard) rebuildLogFiles() {
	var logFiles []TailedFile
	if m.config != nil {
		for _, log := range m.config.Logs {
			exists := false
			readable := false
			if info, err := os.Stat(log.Path); err == nil {
				exists = true
				readable = !info.IsDir()
			}
			logFiles = append(logFiles, TailedFile{
				Path:     log.Path,
				Exists:   exists,
				Readable: readable,
			})
		}
	}
	m.tailedFiles = logFiles
}

// View renders the current view
func (m Dashboard) View() string {
	if m.quitting {
		return ""
	}

	switch m.currentView {
	case viewConfig:
		return m.renderConfigView()
	case viewConfigEdit:
		if m.configEditor != nil {
			return m.configEditor.View()
		}
		return m.renderConfigView()
	case viewEvents:
		return m.renderEventsView()
	case viewTest:
		return m.renderTestView()
	case viewSetup:
		if m.setupWizard != nil {
			return m.setupWizard.View()
		}
		return m.renderDashboard()
	case viewUninstall:
		return m.renderUninstallView()
	default:
		return m.renderDashboard()
	}
}

// renderDashboard renders the main dashboard view
func (m Dashboard) renderDashboard() string {
	header := lipgloss.JoinHorizontal(
		lipgloss.Top,
		TitleStyle.Render("YAAT Sidecar v1.2.0"),
		"  ",
		StatusBadge(func() string {
			if m.isRunning {
				return "Running"
			}
			return "Stopped"
		}(), m.isRunning),
	)

	sections := []string{
		m.renderServiceSection(),
		m.renderConnectivitySection(),
		m.renderDeliverySection(),
		m.renderLogFilesSection(),
	}

	content := header + "\n\n" + strings.Join(sections, "\n\n")

	if m.message != "" {
		content += "\n\n" + SuccessStyle.Render(m.message)
	}

	if m.stateError != nil {
		content += "\n\n" + WarningStyle.Render(fmt.Sprintf("State data unavailable: %v", m.stateError))
	}

	content += "\n\n" + renderHelp()

	return BaseStyle.Render(content) + "\n"
}

func (m Dashboard) renderServiceSection() string {
	var b strings.Builder
	b.WriteString(SectionHeaderStyle.Render("Service") + "\n")

	if m.configError != nil {
		b.WriteString(ErrorStyle.Render("  No configuration detected") + "\n")
		if m.configPath != "" {
			b.WriteString(MutedStyle.Render("  Last known: "+m.configPath) + "\n")
		}
		b.WriteString(MutedStyle.Render("  Run `yaat-sidecar --setup` to create one") + "\n")
	} else {
		b.WriteString(MetricRow("Service", m.config.ServiceName, true) + "\n")
		b.WriteString(MetricRow("Environment", m.config.Environment, false) + "\n")
		b.WriteString(MetricRow("Config path", m.configPath, false) + "\n")
	}

	statusLabel := "Stopped"
	highlight := false
	if m.isRunning {
		statusLabel = fmt.Sprintf("Running (%s)", m.uptimeString())
		highlight = true
	}
	b.WriteString(MetricRow("Daemon", statusLabel, highlight) + "\n")

	defaultLogPath := "/var/log/yaat-sidecar.log"
	logPath := daemon.GetLogPath(defaultLogPath)
	if logPath == "" {
		logPath = daemon.GetExpectedLogPath(defaultLogPath) + " (pending)"
	}
	b.WriteString(MetricRow("Log file", logPath, false) + "\n")

	metricsStatus := "disabled"
	metricsHighlight := false
	statsdStatus := "disabled"
	if m.config != nil && m.config.Metrics.Enabled {
		metricsStatus = fmt.Sprintf("enabled (%s)", m.config.Metrics.Interval)
		metricsHighlight = true
		if m.config.Metrics.StatsD.Enabled {
			statsdStatus = fmt.Sprintf("listening on %s", m.config.Metrics.StatsD.ListenAddr)
		}
	}
	b.WriteString(MetricRow("Host metrics", metricsStatus, metricsHighlight) + "\n")
	b.WriteString(MetricRow("StatsD listener", statsdStatus, m.config != nil && m.config.Metrics.StatsD.Enabled) + "\n")

	return b.String()
}

func (m Dashboard) renderConnectivitySection() string {
	var b strings.Builder
	b.WriteString(SectionHeaderStyle.Render("Connectivity") + "\n")

	if m.lastTest.RanAt.IsZero() {
		b.WriteString(MutedStyle.Render("  No connectivity tests recorded yet.") + "\n")
		b.WriteString(MutedStyle.Render("  Run `yaat-sidecar --test` or press 't' to send sample events.") + "\n")
		return b.String()
	}

	status := SuccessStyle.Render("  ✓ Successful")
	if !m.lastTest.Success {
		status = ErrorStyle.Render("  ✗ Failed")
	}

	relative := formatRelativeTime(m.lastTest.RanAt)
	if relative != "" {
		status += MutedStyle.Render(" • " + relative)
	}
	b.WriteString(status + "\n")

	b.WriteString(MetricRow("Endpoint", m.lastTest.Endpoint, false) + "\n")
	if m.lastTest.LatencyMillis > 0 {
		b.WriteString(MetricRow("Latency", fmt.Sprintf("%d ms", m.lastTest.LatencyMillis), false) + "\n")
	}
	b.WriteString(MetricRow("Events sent", fmt.Sprintf("%d", len(m.lastTest.Events)), false) + "\n")

	if m.lastTest.Error != "" {
		b.WriteString(ErrorStyle.Render("  "+m.lastTest.Error) + "\n")
	}

	return b.String()
}

func (m Dashboard) renderLogFilesSection() string {
	var b strings.Builder
	b.WriteString(SectionHeaderStyle.Render("Log Sources") + "\n")

	if len(m.tailedFiles) == 0 {
		b.WriteString(MutedStyle.Render("  No log files configured yet.") + "\n")
		b.WriteString(MutedStyle.Render("  Press 's' to run the setup wizard.") + "\n")
		return b.String()
	}

	for _, file := range m.tailedFiles {
		status, note := formatLogFileStatus(file)
		line := fmt.Sprintf("  %s %s", status, ValueStyle.Render(file.Path))
		if note != "" {
			line += " " + MutedStyle.Render(note)
		}
		b.WriteString(line + "\n")
	}

	return b.String()
}

func (m Dashboard) renderDeliverySection() string {
	var b strings.Builder
	b.WriteString(SectionHeaderStyle.Render("Delivery") + "\n")

	snap := m.diagSnapshot
	b.WriteString(MetricRow("Queue length", fmt.Sprintf("%d", snap.QueueLength), false) + "\n")
	b.WriteString(MetricRow("In-memory queue", fmt.Sprintf("%d", snap.InMemoryQueue), false) + "\n")
	b.WriteString(MetricRow("Persisted queue", fmt.Sprintf("%d", snap.PersistedQueue), false) + "\n")
	b.WriteString(MetricRow("Dead-letter queue", fmt.Sprintf("%d", snap.DeadLetterQueue), false) + "\n")
	b.WriteString(MetricRow("Events sent", fmt.Sprintf("%d", snap.TotalEventsSent), false) + "\n")
	if snap.TotalEventsFailed > 0 {
		b.WriteString(MetricRow("Events failed", fmt.Sprintf("%d", snap.TotalEventsFailed), false) + "\n")
	}
	b.WriteString(MetricRow("Throughput (events/min)", fmt.Sprintf("%.1f", snap.ThroughputPerMin), false) + "\n")
	if !snap.LastSuccessAt.IsZero() {
		b.WriteString(MetricRow("Last success", formatRelativeTime(snap.LastSuccessAt), false) + "\n")
	}
	if !snap.LastFailureAt.IsZero() {
		b.WriteString(MetricRow("Last failure", formatRelativeTime(snap.LastFailureAt), false) + "\n")
	}
	if snap.LastError != "" {
		b.WriteString(ErrorStyle.Render("  "+snap.LastError) + "\n")
	}

	return b.String()
}

// renderConfigView renders the configuration view
func (m Dashboard) renderConfigView() string {
	header := TitleStyle.Render("Configuration") + "\n\n"

	var content string

	if m.configError != nil {
		content = ErrorStyle.Render("No configuration file found") + "\n\n"
		content += MutedStyle.Render("Searched locations:") + "\n"
		content += MutedStyle.Render("  • ./yaat.yaml") + "\n"
		content += MutedStyle.Render("  • ~/.yaat/yaat.yaml") + "\n"
		content += MutedStyle.Render("  • ~/.config/yaat/yaat.yaml") + "\n"
		content += MutedStyle.Render("  • /etc/yaat/yaat.yaml") + "\n\n"
		content += SuccessStyle.Render("Run 'yaat-sidecar --setup' to create configuration") + "\n"
		content += MutedStyle.Render("Press 'Enter' to create one here, or 'c' to return.") + "\n"
	} else {
		content = MutedStyle.Render("Configuration file: ") + ValueStyle.Render(m.configPath) + "\n\n"
		content += LabelStyle.Render("api_key:       ") + ValueStyle.Render(maskAPIKey(m.config.APIKey)) + "\n"
		content += LabelStyle.Render("service_name:  ") + ValueStyle.Render(m.config.ServiceName) + "\n"
		content += LabelStyle.Render("environment:   ") + ValueStyle.Render(m.config.Environment) + "\n"
		content += LabelStyle.Render("api_endpoint:  ") + ValueStyle.Render(m.config.APIEndpoint) + "\n"
		content += LabelStyle.Render("buffer_size:   ") + ValueStyle.Render(fmt.Sprintf("%d", m.config.BufferSize)) + "\n"
		content += LabelStyle.Render("flush_interval:") + ValueStyle.Render(m.config.FlushInterval) + "\n\n"
		content += LabelStyle.Render("delivery.batch_size:     ") + ValueStyle.Render(fmt.Sprintf("%d", m.config.Delivery.BatchSize)) + "\n"
		content += LabelStyle.Render("delivery.compress:       ") + ValueStyle.Render(fmt.Sprintf("%t", m.config.Delivery.Compress)) + "\n"
		content += LabelStyle.Render("delivery.max_batch_bytes:") + ValueStyle.Render(fmt.Sprintf("%d", m.config.Delivery.MaxBatchBytes)) + "\n"
		content += LabelStyle.Render("delivery.queue_retention: ") + ValueStyle.Render(m.config.Delivery.QueueRetention) + "\n"
		content += LabelStyle.Render("delivery.dead_letter_retention: ") + ValueStyle.Render(m.config.Delivery.DeadLetterRetention) + "\n\n"
		content += LabelStyle.Render("metrics.enabled:           ") + ValueStyle.Render(fmt.Sprintf("%t", m.config.Metrics.Enabled)) + "\n"
		content += LabelStyle.Render("metrics.interval:          ") + ValueStyle.Render(m.config.Metrics.Interval) + "\n"
		if len(m.config.Metrics.Tags) > 0 {
			tagPairs := make([]string, 0, len(m.config.Metrics.Tags))
			for k, v := range m.config.Metrics.Tags {
				tagPairs = append(tagPairs, fmt.Sprintf("%s=%s", k, v))
			}
			content += LabelStyle.Render("metrics.tags:              ") + ValueStyle.Render(strings.Join(tagPairs, ", ")) + "\n"
		}
		content += LabelStyle.Render("metrics.statsd.enabled:    ") + ValueStyle.Render(fmt.Sprintf("%t", m.config.Metrics.StatsD.Enabled)) + "\n"
		content += LabelStyle.Render("metrics.statsd.listen_addr:") + ValueStyle.Render(m.config.Metrics.StatsD.ListenAddr) + "\n"
		content += LabelStyle.Render("metrics.statsd.namespace:  ") + ValueStyle.Render(m.config.Metrics.StatsD.Namespace) + "\n"
		if len(m.config.Metrics.StatsD.Tags) > 0 {
			statsdPairs := make([]string, 0, len(m.config.Metrics.StatsD.Tags))
			for k, v := range m.config.Metrics.StatsD.Tags {
				statsdPairs = append(statsdPairs, fmt.Sprintf("%s=%s", k, v))
			}
			content += LabelStyle.Render("metrics.statsd.tags:       ") + ValueStyle.Render(strings.Join(statsdPairs, ", ")) + "\n"
		}
		content += "\n"

		if m.config.Proxy.Enabled {
			content += LabelStyle.Render("proxy:         ") + SuccessStyle.Render("enabled") + "\n"
			content += LabelStyle.Render("  listen_port: ") + ValueStyle.Render(fmt.Sprintf("%d", m.config.Proxy.ListenPort)) + "\n"
			content += LabelStyle.Render("  upstream:    ") + ValueStyle.Render(m.config.Proxy.UpstreamURL) + "\n\n"
		}

		content += LabelStyle.Render(fmt.Sprintf("log_files:     ")) + ValueStyle.Render(fmt.Sprintf("%d configured", len(m.config.Logs))) + "\n"
		content += "\n" + MutedStyle.Render("Press 'Enter' to edit, 'c' to return to dashboard") + "\n"
	}

	return BaseStyle.Render(header+content) + "\n"
}

// renderEventsView renders the live events feed
func (m Dashboard) renderEventsView() string {
	header := TitleStyle.Render("Latest Test Events") + "\n\n"
	var body strings.Builder

	if m.lastTest.RanAt.IsZero() {
		body.WriteString(MutedStyle.Render("No test events available yet.") + "\n")
		body.WriteString(MutedStyle.Render("Run `yaat-sidecar --test` or press 't' from the dashboard.") + "\n")
	} else {
		summary := fmt.Sprintf("Captured %d event(s) %s", len(m.lastTest.Events), formatRelativeTime(m.lastTest.RanAt))
		if m.lastTest.Success {
			body.WriteString(SuccessStyle.Render(summary) + "\n\n")
		} else {
			body.WriteString(ErrorStyle.Render(summary) + "\n\n")
		}

		if len(m.lastTest.Events) == 0 {
			body.WriteString(MutedStyle.Render("The last test run did not emit any events.") + "\n")
		}

		for idx, evt := range m.lastTest.Events {
			body.WriteString(formatTestEvent(idx, evt))
		}
	}

	body.WriteString("\n" + MutedStyle.Render("Press 'e' to return to dashboard") + "\n")

	return BaseStyle.Render(header+body.String()) + "\n"
}

// renderTestView renders the test results view
func (m Dashboard) renderTestView() string {
	header := TitleStyle.Render("Configuration Test") + "\n\n"

	var content strings.Builder

	if !m.lastTest.RanAt.IsZero() {
		status := SuccessStyle.Render("✓ Success")
		if !m.lastTest.Success {
			status = ErrorStyle.Render("✗ Failed")
		}
		content.WriteString(fmt.Sprintf("%s • %s • %d event(s)\n", status, formatRelativeTime(m.lastTest.RanAt), len(m.lastTest.Events)))
		if m.lastTest.LatencyMillis > 0 {
			content.WriteString(MutedStyle.Render(fmt.Sprintf("Latency: %d ms", m.lastTest.LatencyMillis)) + "\n")
		}
		if m.lastTest.Error != "" {
			content.WriteString(ErrorStyle.Render(m.lastTest.Error) + "\n")
		}
		content.WriteString("\n")
	}

	if len(m.testResults) == 0 {
		content.WriteString(MutedStyle.Render("Running tests...") + "\n")
	} else {
		for _, test := range m.testResults {
			var statusText string
			if test.Status == "pass" {
				statusText = SuccessStyle.Render("✓ PASS")
			} else {
				statusText = ErrorStyle.Render("✗ FAIL")
			}

			content.WriteString(fmt.Sprintf("%-30s %s\n", test.Name, statusText))
			if test.Detail != "" {
				content.WriteString(MutedStyle.Render(fmt.Sprintf("  %s", test.Detail)) + "\n")
			}
			content.WriteString("\n")
		}
	}

	content.WriteString(MutedStyle.Render("Press 't' to return to dashboard") + "\n")

	return BaseStyle.Render(header+content.String()) + "\n"
}

// renderUninstallView renders the uninstall confirmation and results
func (m Dashboard) renderUninstallView() string {
	header := TitleStyle.Render("Uninstall YAAT Sidecar") + "\n\n"

	var content strings.Builder

	if m.uninstallResult == "" {
		// Show confirmation prompt
		content.WriteString(WarningStyle.Render("⚠ WARNING: This will completely remove YAAT Sidecar") + "\n\n")
		content.WriteString(MutedStyle.Render("The following will be removed:") + "\n")
		content.WriteString(MutedStyle.Render("  • Binary: /usr/local/bin/yaat-sidecar") + "\n")
		content.WriteString(MutedStyle.Render("  • Configuration files") + "\n")
		content.WriteString(MutedStyle.Render("  • State and queue directories") + "\n")
		content.WriteString(MutedStyle.Render("  • System directories (/var/lib/yaat, /var/log/yaat)") + "\n")
		content.WriteString(MutedStyle.Render("  • Launch daemon/agent (systemd service or launchd plist)") + "\n\n")
		content.WriteString(ErrorStyle.Render("This action cannot be undone!") + "\n\n")
		content.WriteString(MutedStyle.Render("Are you sure you want to uninstall? ") + "\n")
		content.WriteString(KeyStyle.Render("y") + MutedStyle.Render(" Yes, uninstall  ") + "\n")
		content.WriteString(KeyStyle.Render("n") + MutedStyle.Render(" No, cancel") + "\n")
	} else {
		// Show uninstall results
		if m.uninstallResult == "success" {
			content.WriteString(SuccessStyle.Render("✓ Uninstall completed successfully") + "\n\n")
			content.WriteString(MutedStyle.Render("YAAT Sidecar has been completely removed from your system.") + "\n")
		} else if m.uninstallResult == "error" {
			content.WriteString(ErrorStyle.Render("✗ Uninstall failed") + "\n\n")
			content.WriteString(MutedStyle.Render("Fatal errors occurred during uninstallation:") + "\n\n")
			for _, warning := range m.uninstallWarnings {
				content.WriteString(ErrorStyle.Render("  • "+warning) + "\n")
			}
			content.WriteString("\n" + MutedStyle.Render("Please review the errors above and try again.") + "\n")
		} else {
			content.WriteString(WarningStyle.Render("⚠ Uninstall completed with warnings") + "\n\n")
			content.WriteString(MutedStyle.Render("Some items could not be removed:") + "\n\n")
			for _, warning := range m.uninstallWarnings {
				content.WriteString(WarningStyle.Render("  • "+warning) + "\n")
			}
			content.WriteString("\n" + MutedStyle.Render("You may need to remove these manually with appropriate permissions.") + "\n")
		}
		content.WriteString("\n" + MutedStyle.Render("Press 'q' to exit") + "\n")
	}

	return BaseStyle.Render(header+content.String()) + "\n"
}

// runTests runs actual configuration tests
func (m *Dashboard) runTests() {
	m.testResults = []TestResult{}

	// Test 1: Configuration file
	if m.configError != nil {
		m.testResults = append(m.testResults, TestResult{
			Name:   "Configuration File",
			Status: "fail",
			Detail: "No configuration file found",
		})
		return
	}

	m.testResults = append(m.testResults, TestResult{
		Name:   "Configuration File",
		Status: "pass",
		Detail: fmt.Sprintf("Loaded from %s", m.configPath),
	})

	// Test 2: API Key
	if m.config.APIKey == "" {
		m.testResults = append(m.testResults, TestResult{
			Name:   "API Key",
			Status: "fail",
			Detail: "API key is empty",
		})
	} else {
		m.testResults = append(m.testResults, TestResult{
			Name:   "API Key",
			Status: "pass",
			Detail: "API key is set",
		})
	}

	// Test 3: Log Files
	readable := 0
	for _, log := range m.tailedFiles {
		if log.Readable {
			readable++
		}
	}

	if len(m.tailedFiles) == 0 {
		m.testResults = append(m.testResults, TestResult{
			Name:   "Log Files",
			Status: "fail",
			Detail: "No log files configured",
		})
	} else if readable == 0 {
		m.testResults = append(m.testResults, TestResult{
			Name:   "Log Files",
			Status: "fail",
			Detail: fmt.Sprintf("0/%d files readable", len(m.tailedFiles)),
		})
	} else {
		m.testResults = append(m.testResults, TestResult{
			Name:   "Log Files",
			Status: "pass",
			Detail: fmt.Sprintf("%d/%d files readable", readable, len(m.tailedFiles)),
		})
	}

	// Test 4: API Connectivity
	opts := forwarder.Options{
		BatchSize:     m.config.Delivery.BatchSize,
		Compress:      m.config.Delivery.Compress,
		MaxBatchBytes: m.config.Delivery.MaxBatchBytes,
	}
	fwd := forwarder.NewWithOptions(m.config.APIEndpoint, m.config.APIKey, opts)
	report, err := fwd.Test(m.config.ServiceName, m.config.Environment, m.config.Tags)

	var (
		latency time.Duration
		events  []buffer.Event
	)
	if report != nil {
		latency = report.Latency
		events = report.Events
	}

	if err != nil {
		m.testResults = append(m.testResults, TestResult{
			Name:   "API Connectivity",
			Status: "fail",
			Detail: fmt.Sprintf("Connection failed: %v", err),
		})
	} else {
		m.testResults = append(m.testResults, TestResult{
			Name:   "API Connectivity",
			Status: "pass",
			Detail: fmt.Sprintf("Sent %d test events in %v", len(events), latency.Truncate(time.Millisecond)),
		})
	}

	latest := state.NewTestResult(m.config.APIEndpoint, m.config.ServiceName, m.config.Environment, events, latency, err)

	if recordErr := state.RecordTest(latest); recordErr != nil {
		m.testResults = append(m.testResults, TestResult{
			Name:   "State Persistence",
			Status: "fail",
			Detail: fmt.Sprintf("Unable to update local state: %v", recordErr),
		})
	} else {
		m.lastTest = latest
	}
}

// renderHelp renders the help footer
func renderHelp() string {
	keys := []struct {
		key  string
		desc string
	}{
		{"s", "Setup"},
		{"c", "Config"},
		{"e", "Events"},
		{"t", "Test"},
		{"u", "Uninstall"},
		{"q", "Quit"},
	}

	var parts []string
	for _, k := range keys {
		keyText := KeyStyle.Render(k.key)
		descText := KeyDescStyle.Render(k.desc)
		parts = append(parts, keyText+" "+descText)
	}

	return strings.Join(parts, "  ")
}

func formatLogFileStatus(file TailedFile) (string, string) {
	switch {
	case !file.Exists:
		return ErrorStyle.Render("✗"), "not found"
	case !file.Readable:
		return WarningStyle.Render("⚠"), "permission denied"
	default:
		return SuccessStyle.Render("✓"), ""
	}
}

func formatRelativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	diff := time.Since(t)
	if diff < time.Minute {
		return "just now"
	}
	if diff < time.Hour {
		return fmt.Sprintf("%dm ago", int(diff.Minutes()))
	}
	if diff < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(diff.Hours()))
	}
	days := int(diff.Hours() / 24)
	if days == 1 {
		return "yesterday"
	}
	return fmt.Sprintf("%dd ago", days)
}

func formatTestEvent(idx int, evt state.TestEvent) string {
	var b strings.Builder

	ts := evt.Timestamp.Local().Format("15:04:05")
	eventType := strings.ToUpper(evt.EventType)
	if eventType == "" {
		eventType = "LOG"
	}

	b.WriteString(fmt.Sprintf("%2d. [%s] %-5s", idx+1, ts, eventType))

	switch evt.EventType {
	case "log":
		if evt.Level != "" {
			b.WriteString(" " + strings.ToUpper(evt.Level))
		}
	case "span":
		if evt.StatusCode != 0 {
			b.WriteString(fmt.Sprintf(" %d", evt.StatusCode))
		}
	}

	summary := summariseEvent(evt)
	if summary != "" {
		b.WriteString(" " + ValueStyle.Render(summary))
	}
	b.WriteString("\n")

	if len(evt.Tags) > 0 {
		b.WriteString(MutedStyle.Render("      tags: "+formatTags(evt.Tags)) + "\n")
	}

	if evt.Stacktrace != "" {
		b.WriteString(MutedStyle.Render("      stack: "+truncate(evt.Stacktrace, 80)) + "\n")
	}

	return b.String()
}

func summariseEvent(evt state.TestEvent) string {
	switch evt.EventType {
	case "metric":
		if evt.MetricName != "" {
			return fmt.Sprintf("%s=%.2f", evt.MetricName, evt.MetricValue)
		}
		return fmt.Sprintf("%.2f", evt.MetricValue)
	case "span":
		parts := []string{}
		if evt.Operation != "" {
			parts = append(parts, evt.Operation)
		}
		if evt.DurationMs > 0 {
			parts = append(parts, fmt.Sprintf("%.1fms", evt.DurationMs))
		}
		if len(parts) == 0 {
			return "span event"
		}
		return strings.Join(parts, " • ")
	default:
		if evt.Message != "" {
			return evt.Message
		}
		return "log event"
	}
}

func formatTags(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, tags[k]))
	}
	return strings.Join(pairs, ", ")
}

func truncate(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	if limit <= 3 {
		return s[:limit]
	}
	return s[:limit-3] + "..."
}

// Helper methods

func (m *Dashboard) uptimeString() string {
	if !m.isRunning {
		return "--"
	}

	hours := int(m.uptime.Hours())
	minutes := int(m.uptime.Minutes()) % 60
	seconds := int(m.uptime.Seconds()) % 60

	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	} else if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func maskAPIKey(key string) string {
	if len(key) < 10 {
		return "***"
	}
	return key[:7] + "***************"
}

// Tick message for periodic updates
type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// RunDashboard starts the TUI dashboard
func RunDashboard() error {
	p := tea.NewProgram(NewDashboard(), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
