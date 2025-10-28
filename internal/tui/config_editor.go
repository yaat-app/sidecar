package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/yaat-app/sidecar/internal/config"
)

type ConfigEditor struct {
	inputs         []textinput.Model
	focusIndex     int
	compress       bool
	metricsEnabled bool
	statsdEnabled  bool
	scrubEnabled   bool

	logEntries   []config.LogConfig
	newLogPath   textinput.Model
	newLogFormat textinput.Model

	err        error
	done       bool
	saved      bool
	cancelled  bool
	configPath string

	working   config.Config
	savedPath string
}

const (
	fieldAPIKey = iota
	fieldServiceName
	fieldEnvironment
	fieldAPIEndpoint
	fieldBufferSize
	fieldFlushInterval
	fieldBatchSize
	fieldMetricsInterval
	fieldStatsdAddr
	totalTextInputs
)

func NewConfigEditor(cfg *config.Config, path string) *ConfigEditor {
	var base config.Config
	if cfg != nil {
		base = *cfg
	} else {
		base = config.Config{
			APIEndpoint:   "https://yaat.io/api/v1/ingest",
			ServiceName:   "",
			APIKey:        "",
			Environment:   "production",
			BufferSize:    1000,
			FlushInterval: "10s",
			Delivery: config.DeliveryConfig{
				BatchSize:           500,
				Compress:            true,
				MaxBatchBytes:       0,
				QueueRetention:      "24h",
				DeadLetterRetention: "168h",
			},
			Metrics: config.MetricsConfig{
				Enabled:  false,
				Interval: "30s",
				StatsD: config.StatsDConfig{
					Enabled:    false,
					ListenAddr: ":8125",
				},
			},
			Scrubbing: config.ScrubbingConfig{
				Enabled: true,
				Rules:   config.RecommendedScrubRules(),
			},
		}
	}

	editor := &ConfigEditor{
		compress:       base.Delivery.Compress,
		metricsEnabled: base.Metrics.Enabled,
		statsdEnabled:  base.Metrics.StatsD.Enabled,
		scrubEnabled:   base.Scrubbing.Enabled || len(base.Scrubbing.Rules) > 0,
		configPath:     path,
		working:        base,
		logEntries:     append([]config.LogConfig(nil), base.Logs...),
	}

	// Build inputs
	editor.inputs = make([]textinput.Model, totalTextInputs)

	apiKey := textinput.New()
	apiKey.Placeholder = "yaat_..."
	apiKey.SetValue(base.APIKey)
	apiKey.EchoMode = textinput.EchoPassword
	apiKey.EchoCharacter = '*'
	apiKey.Width = 48

	service := textinput.New()
	service.Placeholder = "my-service"
	service.SetValue(base.ServiceName)
	service.Width = 48

	env := textinput.New()
	env.Placeholder = "production"
	env.SetValue(base.Environment)
	env.Width = 32

	endpoint := textinput.New()
	endpoint.Placeholder = "https://yaat.io/api/v1/ingest"
	endpoint.SetValue(base.APIEndpoint)
	endpoint.Width = 60

	buffer := textinput.New()
	buffer.Placeholder = "1000"
	buffer.SetValue(fmt.Sprintf("%d", base.BufferSize))
	buffer.Width = 12

	flush := textinput.New()
	flush.Placeholder = "10s"
	flush.SetValue(base.FlushInterval)
	flush.Width = 12

	batch := textinput.New()
	batch.Placeholder = "500"
	if base.Delivery.BatchSize > 0 {
		batch.SetValue(fmt.Sprintf("%d", base.Delivery.BatchSize))
	}
	batch.Width = 12

	metricsInt := textinput.New()
	metricsInt.Placeholder = "30s"
	metricsInt.SetValue(base.Metrics.Interval)
	metricsInt.Width = 12

	statsdAddr := textinput.New()
	statsdAddr.Placeholder = ":8125"
	statsdAddr.SetValue(base.Metrics.StatsD.ListenAddr)
	statsdAddr.Width = 20

	editor.inputs[fieldAPIKey] = apiKey
	editor.inputs[fieldServiceName] = service
	editor.inputs[fieldEnvironment] = env
	editor.inputs[fieldAPIEndpoint] = endpoint
	editor.inputs[fieldBufferSize] = buffer
	editor.inputs[fieldFlushInterval] = flush
	editor.inputs[fieldBatchSize] = batch
	editor.inputs[fieldMetricsInterval] = metricsInt
	editor.inputs[fieldStatsdAddr] = statsdAddr

	editor.inputs[fieldAPIKey].Focus()

	pathInput := textinput.New()
	pathInput.Placeholder = "/var/log/myapp/app.log"
	pathInput.Width = 48

	formatInput := textinput.New()
	formatInput.Placeholder = "django | nginx | apache | json | docker"
	formatInput.Width = 32
	formatInput.SetValue("json")

	editor.newLogPath = pathInput
	editor.newLogFormat = formatInput

	return editor
}

func (e *ConfigEditor) Update(msg tea.Msg) tea.Cmd {
	if e.done {
		return nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "ctrl+i":
			e.advanceFocus(1)
			return nil
		case "shift+tab":
			e.advanceFocus(-1)
			return nil
		case "esc":
			e.done = true
			e.cancelled = true
			return nil
		case " ":
			if idx, ok := e.toggleIndex(); ok {
				e.toggleBool(idx)
				return nil
			}
		case "ctrl+s":
			if err := e.save(); err != nil {
				e.err = err
			}
			return nil
		case "ctrl+n":
			e.focusIndex = e.addPathIndex()
			e.setFocus(e.focusIndex)
			e.newLogPath.SetValue("")
			e.newLogFormat.SetValue("json")
			return nil
		case "backspace", "delete":
			if idx, ok := e.logListIndex(); ok && idx < len(e.logEntries) {
				e.logEntries = append(e.logEntries[:idx], e.logEntries[idx+1:]...)
				if idx > 0 && idx == len(e.logEntries) {
					e.focusIndex--
				}
				e.ensureFocusBounds()
				return nil
			}
		case "enter":
			if idx, ok := e.logListIndex(); ok && idx < len(e.logEntries) {
				entry := e.logEntries[idx]
				e.logEntries = append(e.logEntries[:idx], e.logEntries[idx+1:]...)
				e.newLogPath.SetValue(entry.Path)
				e.newLogFormat.SetValue(entry.Format)
				e.focusIndex = e.addPathIndex()
				e.setFocus(e.focusIndex)
				return nil
			}
			if e.focusIndex == e.addFormatIndex() {
				if err := e.appendLogFromInputs(); err != nil {
					e.err = err
				}
				return nil
			}
		}

		switch {
		case e.focusIndex < totalTextInputs:
			var cmd tea.Cmd
			e.inputs[e.focusIndex], cmd = e.inputs[e.focusIndex].Update(msg)
			return cmd
		case e.focusIndex == e.addPathIndex():
			var cmd tea.Cmd
			e.newLogPath, cmd = e.newLogPath.Update(msg)
			return cmd
		case e.focusIndex == e.addFormatIndex():
			var cmd tea.Cmd
			e.newLogFormat, cmd = e.newLogFormat.Update(msg)
			return cmd
		}
	default:
		switch {
		case e.focusIndex < totalTextInputs:
			var cmd tea.Cmd
			e.inputs[e.focusIndex], cmd = e.inputs[e.focusIndex].Update(msg)
			return cmd
		case e.focusIndex == e.addPathIndex():
			var cmd tea.Cmd
			e.newLogPath, cmd = e.newLogPath.Update(msg)
			return cmd
		case e.focusIndex == e.addFormatIndex():
			var cmd tea.Cmd
			e.newLogFormat, cmd = e.newLogFormat.Update(msg)
			return cmd
		}
	}

	return nil
}

func (e *ConfigEditor) advanceFocus(delta int) {
	total := e.totalFocusItems()
	if total == 0 {
		return
	}
	e.focusIndex = (e.focusIndex + delta) % total
	if e.focusIndex < 0 {
		e.focusIndex += total
	}
	e.setFocus(e.focusIndex)
}

func (e *ConfigEditor) toggleBool(idx int) {
	switch idx {
	case 0:
		e.compress = !e.compress
	case 1:
		e.metricsEnabled = !e.metricsEnabled
	case 2:
		e.statsdEnabled = !e.statsdEnabled
	case 3:
		e.scrubEnabled = !e.scrubEnabled
		if !e.scrubEnabled && len(e.working.Scrubbing.Rules) == 0 {
			e.working.Scrubbing.Rules = config.RecommendedScrubRules()
		}
	}
}

func (e *ConfigEditor) save() error {
	w := e.working

	apiKey := strings.TrimSpace(e.inputs[fieldAPIKey].Value())
	service := strings.TrimSpace(e.inputs[fieldServiceName].Value())
	environment := strings.TrimSpace(e.inputs[fieldEnvironment].Value())
	apiEndpoint := strings.TrimSpace(e.inputs[fieldAPIEndpoint].Value())
	bufferSize := strings.TrimSpace(e.inputs[fieldBufferSize].Value())
	flushInterval := strings.TrimSpace(e.inputs[fieldFlushInterval].Value())
	batchSize := strings.TrimSpace(e.inputs[fieldBatchSize].Value())
	metricsInterval := strings.TrimSpace(e.inputs[fieldMetricsInterval].Value())
	statsdAddr := strings.TrimSpace(e.inputs[fieldStatsdAddr].Value())

	if apiKey == "" {
		return fmt.Errorf("api_key is required")
	}
	if service == "" {
		return fmt.Errorf("service_name is required")
	}
	if apiEndpoint == "" {
		return fmt.Errorf("api_endpoint is required")
	}

	buf, err := strconv.Atoi(bufferSize)
	if err != nil || buf <= 0 {
		return fmt.Errorf("buffer_size must be a positive integer")
	}

	batch, err := strconv.Atoi(batchSize)
	if err != nil || batch <= 0 {
		return fmt.Errorf("delivery.batch_size must be a positive integer")
	}

	w.APIKey = apiKey
	w.ServiceName = service
	w.Environment = environment
	w.APIEndpoint = apiEndpoint
	w.BufferSize = buf
	w.FlushInterval = flushInterval
	w.Delivery.BatchSize = batch
	w.Delivery.Compress = e.compress

	if metricsInterval == "" {
		metricsInterval = "30s"
	}
	w.Metrics.Enabled = e.metricsEnabled
	w.Metrics.Interval = metricsInterval
	w.Metrics.StatsD.Enabled = e.statsdEnabled
	if statsdAddr == "" {
		statsdAddr = ":8125"
	}
	w.Metrics.StatsD.ListenAddr = statsdAddr

	w.Scrubbing.Enabled = e.scrubEnabled
	if w.Scrubbing.Enabled && len(w.Scrubbing.Rules) == 0 {
		w.Scrubbing.Rules = config.RecommendedScrubRules()
	}

	w.Logs = append([]config.LogConfig(nil), e.logEntries...)

	path := e.configPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	w.SourcePath = path

	if err := config.SaveConfig(path, &w); err != nil {
		return err
	}

	e.working = w
	e.savedPath = path
	e.saved = true
	e.done = true
	return nil
}

func (e *ConfigEditor) View() string {
	var b strings.Builder

	b.WriteString(TitleStyle.Render("Edit Configuration") + "\n\n")

	b.WriteString(LabelStyle.Render("API Key") + "\n")
	b.WriteString("  " + e.inputs[fieldAPIKey].View() + "\n\n")

	b.WriteString(LabelStyle.Render("Service Name") + "\n")
	b.WriteString("  " + e.inputs[fieldServiceName].View() + "\n\n")

	b.WriteString(LabelStyle.Render("Environment") + "\n")
	b.WriteString("  " + e.inputs[fieldEnvironment].View() + "\n\n")

	b.WriteString(LabelStyle.Render("API Endpoint") + "\n")
	b.WriteString("  " + e.inputs[fieldAPIEndpoint].View() + "\n\n")

	b.WriteString(LabelStyle.Render("Buffer Size") + "\n")
	b.WriteString("  " + e.inputs[fieldBufferSize].View() + "\n\n")

	b.WriteString(LabelStyle.Render("Flush Interval") + "\n")
	b.WriteString("  " + e.inputs[fieldFlushInterval].View() + "\n\n")

	b.WriteString(LabelStyle.Render("Delivery Batch Size") + "\n")
	b.WriteString("  " + e.inputs[fieldBatchSize].View() + "\n\n")

	checkbox := func(selected bool, label string, idx int) string {
		cursor := "  "
		if focusIdx, ok := e.toggleIndex(); ok && focusIdx == idx {
			cursor = "▸ "
		}
		box := "[ ]"
		if selected {
			box = "[✓]"
		}
		return cursor + box + " " + label
	}

	b.WriteString(checkbox(e.compress, "Compress payloads (delivery.compress)", 0) + "\n")
	b.WriteString(checkbox(e.metricsEnabled, "Enable host metrics (metrics.enabled)", 1) + "\n")
	b.WriteString(checkbox(e.statsdEnabled, "Enable StatsD listener (metrics.statsd.enabled)", 2) + "\n")
	b.WriteString(checkbox(e.scrubEnabled, "Enable scrubbing rules (scrubbing.enabled)", 3) + "\n\n")

	b.WriteString(LabelStyle.Render("Metrics Interval") + "\n")
	b.WriteString("  " + e.inputs[fieldMetricsInterval].View() + "\n\n")

	b.WriteString(LabelStyle.Render("StatsD Listen Address") + "\n")
	b.WriteString("  " + e.inputs[fieldStatsdAddr].View() + "\n\n")

	b.WriteString(SectionHeaderStyle.Render("Log Sources") + "\n")
	if len(e.logEntries) == 0 {
		b.WriteString(MutedStyle.Render("  No log sources configured yet.") + "\n\n")
	} else {
		for i, log := range e.logEntries {
			cursor := "  "
			if idx, ok := e.logListIndex(); ok && idx == i {
				cursor = SuccessStyle.Render("▸ ")
			}
			line := fmt.Sprintf("%s%s (%s)", cursor, ValueStyle.Render(log.Path), MutedStyle.Render(log.Format))
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(LabelStyle.Render("Add / Edit Log") + "\n")
	pathView := e.newLogPath.View()
	if e.focusIndex == e.addPathIndex() {
		pathView = SuccessStyle.Render(e.newLogPath.View())
	}
	formatView := e.newLogFormat.View()
	if e.focusIndex == e.addFormatIndex() {
		formatView = SuccessStyle.Render(e.newLogFormat.View())
	}
	b.WriteString("  Path: " + pathView + "\n")
	b.WriteString("  Format: " + formatView + "\n\n")

	if e.err != nil {
		b.WriteString(ErrorStyle.Render(fmt.Sprintf("Error: %v", e.err)) + "\n\n")
	}

	b.WriteString(MutedStyle.Render("[Tab] Next  [Shift+Tab] Previous  [Space] Toggle  [Enter] Edit/Add log  [Del] Remove log  [Ctrl+N] New log  [Ctrl+S] Save  [Esc] Cancel") + "\n")

	return BaseStyle.Render(b.String())
}

func (e *ConfigEditor) totalFocusItems() int {
	return totalTextInputs + 4 + len(e.logEntries) + 2
}

func (e *ConfigEditor) toggleIndex() (int, bool) {
	toggleStart := totalTextInputs
	toggleEnd := toggleStart + 4
	if e.focusIndex >= toggleStart && e.focusIndex < toggleEnd {
		return e.focusIndex - toggleStart, true
	}
	return 0, false
}

func (e *ConfigEditor) logListIndex() (int, bool) {
	logStart := totalTextInputs + 4
	logEnd := logStart + len(e.logEntries)
	if e.focusIndex >= logStart && e.focusIndex < logEnd {
		return e.focusIndex - logStart, true
	}
	return 0, false
}

func (e *ConfigEditor) addPathIndex() int {
	return totalTextInputs + 4 + len(e.logEntries)
}

func (e *ConfigEditor) addFormatIndex() int {
	return e.addPathIndex() + 1
}

func (e *ConfigEditor) setFocus(idx int) {
	for i := 0; i < totalTextInputs; i++ {
		if i == idx {
			e.inputs[i].Focus()
		} else {
			e.inputs[i].Blur()
		}
	}

	if idx == e.addPathIndex() {
		e.newLogPath.Focus()
		e.newLogFormat.Blur()
	} else if idx == e.addFormatIndex() {
		e.newLogFormat.Focus()
		e.newLogPath.Blur()
	} else {
		e.newLogPath.Blur()
		e.newLogFormat.Blur()
	}
}

func (e *ConfigEditor) ensureFocusBounds() {
	total := e.totalFocusItems()
	if total == 0 {
		e.focusIndex = 0
		return
	}
	if e.focusIndex >= total {
		e.focusIndex = total - 1
	}
	e.setFocus(e.focusIndex)
}

func (e *ConfigEditor) appendLogFromInputs() error {
	path := strings.TrimSpace(e.newLogPath.Value())
	format := strings.TrimSpace(strings.ToLower(e.newLogFormat.Value()))
	if path == "" {
		return fmt.Errorf("log path is required")
	}
	if format == "" {
		format = "json"
		e.newLogFormat.SetValue(format)
	}
	e.logEntries = append(e.logEntries, config.LogConfig{
		Path:   path,
		Format: format,
	})
	e.newLogPath.SetValue("")
	e.newLogFormat.SetValue("json")
	e.focusIndex = e.addPathIndex()
	e.setFocus(e.focusIndex)
	return nil
}

func (e *ConfigEditor) Done() bool {
	return e.done
}

func (e *ConfigEditor) Saved() bool {
	return e.saved
}

func (e *ConfigEditor) Cancelled() bool {
	return e.cancelled
}

func (e *ConfigEditor) Error() error {
	return e.err
}

func (e *ConfigEditor) SavedPath() string {
	return e.savedPath
}
