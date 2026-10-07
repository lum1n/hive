package picker

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lum1n/hive/internal/config"
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true)
	plainStyle   = lipgloss.NewStyle()
	mutedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	onlineStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	offlineStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	authStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	busyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	// markerStyle is honey amber, outside the host palette so it never blends in.
	markerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
)

// hostPalette skips red and green so host names never read as a status badge.
var hostPalette = []lipgloss.Color{"6", "5", "4", "3", "14", "13", "12", "11"}

// hostStyle colors a host by its position in the config, so every picker
// shows the same host in the same color.
func hostStyle(hosts []config.Host, id string) lipgloss.Style {
	for i, h := range hosts {
		if h.ID == id {
			return lipgloss.NewStyle().Foreground(hostPalette[i%len(hostPalette)]).Bold(true)
		}
	}
	return plainStyle
}

type seg struct {
	text  string
	style lipgloss.Style
}

// col is one column of a picker row. A zero width takes the rest of the line.
type col struct {
	width int
	segs  []seg
}

func leftWidth(width int) int {
	return min(28, max(8, width/3))
}

// renderRow lays out columns with the shared marker and gaps. The marker is
// the only selection cue; the row itself keeps its normal colors.
func renderRow(selected bool, width int, cols ...col) string {
	marker := "  "
	if selected {
		marker = markerStyle.Render("❯") + " "
	}
	var b strings.Builder
	b.WriteString(marker)
	used := 2
	for i, c := range cols {
		if i > 0 {
			b.WriteString("  ")
			used += 2
		}
		w := c.width
		if w == 0 {
			w = width - used
		}
		if w <= 0 {
			break
		}
		left := w
		for _, s := range c.segs {
			if left <= 0 {
				break
			}
			text := ansi.Truncate(s.text, left, "…")
			left -= ansi.StringWidth(text)
			b.WriteString(s.style.Render(text))
		}
		if c.width > 0 && left > 0 {
			b.WriteString(strings.Repeat(" ", left))
		}
		used += w
	}
	return ansi.Truncate(b.String(), width, "…")
}

func header(title, meta string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, titleStyle.Render(title), "  ", mutedStyle.Render(meta))
}

func filterLine(filter string, cursor bool) string {
	return mutedStyle.Render("  filter> ") + filter + cursorGlyph(cursor)
}

// frame joins the picker sections and clips every line to the terminal.
func frame(width int, parts ...string) string {
	var lines []string
	for _, part := range parts {
		for _, line := range strings.Split(part, "\n") {
			lines = append(lines, ansi.Truncate(line, width, "…"))
		}
	}
	return strings.Join(lines, "\n")
}

func cursorGlyph(show bool) string {
	if show {
		return "█"
	}
	return ""
}
