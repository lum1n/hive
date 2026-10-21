package picker

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/lum1n/hive/internal/agents"
	"github.com/lum1n/hive/internal/config"
)

func TestHostStyleStableByConfigPosition(t *testing.T) {
	t.Parallel()
	var hosts []config.Host
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		hosts = append(hosts, config.Host{ID: id})
	}
	if got := hostStyle(hosts, "b").GetForeground(); got != hostPalette[1] {
		t.Fatalf("host b color = %v, want %v", got, hostPalette[1])
	}
	if got := hostStyle(hosts, "i").GetForeground(); got != hostPalette[0] {
		t.Fatalf("ninth host did not wrap the palette: %v", got)
	}
	if !hostStyle(hosts, "a").GetBold() {
		t.Fatal("configured host is not bold")
	}
	if got := hostStyle(hosts, "missing"); got.GetBold() || got.GetForeground() != plainStyle.GetForeground() {
		t.Fatal("unknown host was styled")
	}
	for _, c := range hostPalette {
		if c == "1" || c == "2" || c == "9" || c == "10" {
			t.Fatalf("host palette contains status color %s", c)
		}
	}
}

func TestLeftWidthBounds(t *testing.T) {
	t.Parallel()
	for width, want := range map[int]int{0: 8, 12: 8, 30: 10, 84: 28, 300: 28} {
		if got := leftWidth(width); got != want {
			t.Errorf("leftWidth(%d) = %d, want %d", width, got, want)
		}
	}
}

func TestRenderRowLayout(t *testing.T) {
	t.Parallel()
	row := ansi.Strip(renderRow(false, 40,
		col{6, []seg{{"ab", plainStyle}, {"/", mutedStyle}, {"c", plainStyle}}},
		col{4, []seg{{"toolong", plainStyle}}},
		col{0, []seg{{"rest", plainStyle}}}))
	if want := "  ab/c    too…  rest"; row != want {
		t.Fatalf("row = %q, want %q", row, want)
	}
	selected := ansi.Strip(renderRow(true, 40, col{0, []seg{{"x", plainStyle}}}))
	if selected != "❯ x" {
		t.Fatalf("selected row = %q", selected)
	}
}

func TestRenderRowFitsWidth(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("界", 40)
	for _, width := range []int{1, 4, 9, 20, 80} {
		row := renderRow(true, width,
			col{leftWidth(width), []seg{{long, plainStyle}}},
			col{14, []seg{{long, mutedStyle}}},
			col{0, []seg{{long, mutedStyle}}})
		if got := ansi.StringWidth(row); got > width {
			t.Fatalf("renderRow width %d overflowed to %d: %q", width, got, ansi.Strip(row))
		}
	}
}

func TestFrameClipsEveryLine(t *testing.T) {
	t.Parallel()
	got := frame(5, "abcdefgh\nab", "", "123456")
	lines := strings.Split(got, "\n")
	if len(lines) != 4 || lines[1] != "ab" || lines[2] != "" {
		t.Fatalf("frame lines = %q", lines)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > 5 {
			t.Fatalf("frame line %q exceeds width", line)
		}
	}
}

func TestStateBadge(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"idle":               "idle",
		"thinking":           "thinking",
		"running-tool":       "tool",
		"waiting-permission": "permission",
		"errored":            "error",
		"unknown":            "…",
		"":                   "…",
	}
	for state, want := range tests {
		if got := stateBadge(agents.Agent{State: state}).text; got != want {
			t.Errorf("stateBadge(%q) = %q, want %q", state, got, want)
		}
	}
	failed := agents.Agent{State: "idle", StateError: &agents.Failure{Code: "protocol"}}
	if got := stateBadge(failed).text; got != "no watcher" {
		t.Fatalf("watcher failure badge = %q", got)
	}
}
