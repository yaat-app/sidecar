package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	primaryColor = lipgloss.Color("#0EA5E9")
	successColor = lipgloss.Color("#22C55E")
	warningColor = lipgloss.Color("#F59E0B")
	errorColor   = lipgloss.Color("#EF4444")
	accentColor  = lipgloss.Color("#6366F1")

	textColor   = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#E5E7EB"}
	labelColor  = lipgloss.AdaptiveColor{Light: "#4B5563", Dark: "#94A3B8"}
	mutedColor  = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#94A3B8"}
	borderColor = lipgloss.Color("#D4D4D8")

	BaseStyle = lipgloss.NewStyle()

	// Title style
	TitleStyle = lipgloss.NewStyle().
			Foreground(primaryColor).
			Bold(true)

	// Section header style
	SectionHeaderStyle = lipgloss.NewStyle().
				Foreground(accentColor).
				Bold(true)

	// Box style
	BoxStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(borderColor).
			Padding(1, 2)

	// Success style
	SuccessStyle = lipgloss.NewStyle().
			Foreground(successColor).
			Bold(false)

	// Error style
	ErrorStyle = lipgloss.NewStyle().
			Foreground(errorColor)

	// Warning style
	WarningStyle = lipgloss.NewStyle().
			Foreground(warningColor)

	// Muted style
	MutedStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	// Label style
	LabelStyle = lipgloss.NewStyle().
			Foreground(labelColor)

	// Value style
	ValueStyle = lipgloss.NewStyle().
			Foreground(textColor)

	// Key style (for keyboard shortcuts)
	KeyStyle = lipgloss.NewStyle().
			Foreground(primaryColor).
			Bold(true)

	// Key description style
	KeyDescStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	// Status indicator styles
	StatusRunningStyle = lipgloss.NewStyle().
				Foreground(successColor).
				Bold(true)

	StatusStoppedStyle = lipgloss.NewStyle().
				Foreground(mutedColor)

	StatusErrorStyle = lipgloss.NewStyle().
				Foreground(errorColor).
				Bold(true)

	// Help style
	HelpStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Padding(1, 0)
)

// StatusBadge creates a colored status badge
func StatusBadge(status string, running bool) string {
	symbol := "●"
	style := StatusRunningStyle

	if !running {
		symbol = "○"
		style = StatusStoppedStyle
	}

	return style.Render(symbol) + " " + style.Render(status)
}

// MetricRow formats a metric row with label and value
func MetricRow(label, value string, highlight bool) string {
	labelText := LabelStyle.Render(padRight(label, 20))

	valueStyle := ValueStyle
	if highlight {
		valueStyle = SuccessStyle
	}

	return labelText + valueStyle.Render(value)
}

// padRight pads a string to a fixed width
func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + lipgloss.NewStyle().Width(width-len(s)).Render("")
}
