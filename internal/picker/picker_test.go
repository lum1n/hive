package picker

import (
	"context"
	"errors"
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lum1n/hive/internal/workspace"
)

func TestMatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		filter string
		target string
		want   bool
	}{
		{"", "devbox/backend", true},
		{"backend", "devbox/backend", true},
		{"db/be", "devbox/backend", true},
		{"xyz", "devbox/backend", false},
		{"DEV", "devbox/backend", true},
	}
	for _, tt := range tests {
		t.Run(tt.filter+"/"+tt.target, func(t *testing.T) {
			t.Parallel()
			if got := match(tt.filter, tt.target); got != tt.want {
				t.Fatalf("match(%q, %q) = %v", tt.filter, tt.target, got)
			}
		})
	}
}

func TestFinishKeepsAttachWhenProgramKilled(t *testing.T) {
	t.Parallel()
	killed := fmt.Errorf("%w: %w", tea.ErrProgramKilled, context.Canceled)
	got, err := finish(model{choice: Choice{
		Action:    ActionAttach,
		Workspace: workspace.ID{Host: "local", Session: "dev"},
	}}, killed)
	if err != nil {
		t.Fatal(err)
	}
	if got.Action != ActionAttach || got.Workspace.Display() != "local/dev" {
		t.Fatalf("%+v", got)
	}
}

func TestFinishQuitStillReturnsKillError(t *testing.T) {
	t.Parallel()
	killed := fmt.Errorf("%w: %w", tea.ErrProgramKilled, context.Canceled)
	got, err := finish(model{choice: Choice{Action: ActionQuit}}, killed)
	if !errors.Is(err, tea.ErrProgramKilled) {
		t.Fatalf("err=%v", err)
	}
	if got.Action != ActionQuit {
		t.Fatalf("%+v", got)
	}
}
