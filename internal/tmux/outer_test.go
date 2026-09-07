package tmux

import (
	"context"
	"strings"
	"testing"

	"github.com/lum1n/hive/internal/execx"
)

func TestMuteAndRestoreOuter(t *testing.T) {
	t.Parallel()
	var cmds []string
	shown := map[string]string{
		"prefix":  "C-b",
		"prefix2": "None",
		"status":  "on",
		"mouse":   "on",
	}
	run := func(ctx context.Context, name string, args ...string) execx.Result {
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		if len(args) >= 2 && args[0] == "show-options" {
			key := args[len(args)-1]
			return execx.Result{Stdout: []byte(shown[key] + "\n")}
		}
		return execx.Result{}
	}
	chrome := ReadOuterChrome(context.Background(), run)
	if chrome.Prefix != "C-b" || chrome.Status != "on" {
		t.Fatalf("%+v", chrome)
	}
	MuteOuter(context.Background(), run)
	RestoreOuter(context.Background(), run, chrome)
	joined := strings.Join(cmds, "\n")
	for _, want := range []string{
		"tmux set-option prefix None",
		"tmux set-option status off",
		"tmux set-option prefix C-b",
		"tmux set-option status on",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in\n%s", want, joined)
		}
	}
}
