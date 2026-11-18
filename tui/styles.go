package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

var (
	// Colors
	colHeader   = lipgloss.Color("#7C3AED")
	colSelected = lipgloss.Color("#A78BFA")
	colNormal   = lipgloss.Color("#E0E0E0")
	colMuted    = lipgloss.Color("#666666")
	colGreen    = lipgloss.Color("#22C55E")
	colRed      = lipgloss.Color("#EF4444")
	colYellow   = lipgloss.Color("#EAB308")
	colBlue     = lipgloss.Color("#3B82F6")
	colCyan     = lipgloss.Color("#06B6D4")
	colBg       = lipgloss.Color("#1A1A2E")
	colBorder   = lipgloss.Color("#333355")
	colWarning  = lipgloss.Color("#F97316")
	colPaused   = lipgloss.Color("#F59E0B")
	colTitle    = lipgloss.Color("#8B5CF6")

	titleStyle = lipgloss.NewStyle().
			Foreground(colTitle).
			Bold(true).
			Padding(0, 1)

	headerBarStyle = lipgloss.NewStyle().
			Background(colHeader).
			Foreground(lipgloss.Color("#FFFFFF")).
			Padding(0, 1).
			Bold(true)

	statusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#16213E")).
			Foreground(colNormal).
			Padding(0, 1)

	connListStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(colBorder).
			Padding(0, 1)

	detailStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(colBorder).
			Padding(0, 1)

	selectedStyle = lipgloss.NewStyle().
			Foreground(colSelected).
			Bold(true)

	normalStyle = lipgloss.NewStyle().
			Foreground(colNormal)

	mutedStyle = lipgloss.NewStyle().
			Foreground(colMuted)

	protoStyle = func(proto string) lipgloss.Style {
		switch proto {
		case "TLS", "HTTPS":
			return lipgloss.NewStyle().Foreground(colGreen).Bold(true)
		case "HTTP":
			return lipgloss.NewStyle().Foreground(colBlue).Bold(true)
		case "DNS":
			return lipgloss.NewStyle().Foreground(colYellow).Bold(true)
		case "QUIC":
			return lipgloss.NewStyle().Foreground(colCyan).Bold(true)
		case "UDP":
			return lipgloss.NewStyle().Foreground(colMuted)
		default:
			return lipgloss.NewStyle().Foreground(colNormal)
		}
	}

	alertStyle = lipgloss.NewStyle().
			Foreground(colWarning).
			Bold(true)

	pausedStyle = lipgloss.NewStyle().
			Foreground(colPaused).
			Bold(true).
			Background(lipgloss.Color("#3D2E00"))

	helpStyle = lipgloss.NewStyle().
			Foreground(colMuted).
			Padding(1)

	directionStyle = func(isInbound bool) lipgloss.Style {
		if isInbound {
			return lipgloss.NewStyle().Foreground(colGreen)
		}
		return lipgloss.NewStyle().Foreground(colRed)
	}

	tabStyle = lipgloss.NewStyle().
			Padding(0, 2).
			Foreground(colMuted)

	activeTabStyle = lipgloss.NewStyle().
			Padding(0, 2).
			Foreground(colSelected).
			Bold(true).
			Underline(true)

	hexStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A8D8EA")).
			Background(lipgloss.Color("#1A1A2E"))

	dialogStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(colHeader).
			Padding(1, 2).
			Width(50)

	dialogTitle = lipgloss.NewStyle().
			Foreground(colTitle).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(colGreen).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(colRed).
			Bold(true)
)

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func formatBytes(b uint64) string {
	switch {
	case b >= 1<<30:
		return formatFloat(float64(b)/(1<<30)) + "GB"
	case b >= 1<<20:
		return formatFloat(float64(b)/(1<<20)) + "MB"
	case b >= 1<<10:
		return formatFloat(float64(b)/(1<<10)) + "KB"
	default:
		return formatFloat(float64(b)) + "B"
	}
}

func formatBytesPerSec(b float64) string {
	switch {
	case b >= 1<<30:
		return formatFloat(b/(1<<30)) + "GB/s"
	case b >= 1<<20:
		return formatFloat(b/(1<<20)) + "MB/s"
	case b >= 1<<10:
		return formatFloat(b/(1<<10)) + "KB/s"
	default:
		return formatFloat(b) + "B/s"
	}
}

func formatFloat(f float64) string {
	if f < 10 {
		return fmt.Sprintf("%.1f", f)
	}
	return fmt.Sprintf("%.0f", f)
}
