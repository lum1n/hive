package tmux

import (
	"context"
	"strings"

	"github.com/lum1n/hive/internal/execx"
)

// OuterChrome is the enclosing tmux session's key/status settings.
type OuterChrome struct {
	Prefix  string
	Prefix2 string
	Status  string
	Mouse   string
}

func ReadOuterChrome(ctx context.Context, run execx.Runner) OuterChrome {
	if run == nil {
		run = execx.Default
	}
	return OuterChrome{
		Prefix:  show(ctx, run, "prefix"),
		Prefix2: show(ctx, run, "prefix2"),
		Status:  show(ctx, run, "status"),
		Mouse:   show(ctx, run, "mouse"),
	}
}

func MuteOuter(ctx context.Context, run execx.Runner) {
	if run == nil {
		run = execx.Default
	}
	_ = set(ctx, run, "prefix", "None")
	_ = set(ctx, run, "prefix2", "None")
	_ = set(ctx, run, "status", "off")
	_ = set(ctx, run, "mouse", "off")
}

func RestoreOuter(ctx context.Context, run execx.Runner, chrome OuterChrome) {
	if run == nil {
		run = execx.Default
	}
	restore(ctx, run, "prefix", chrome.Prefix)
	restore(ctx, run, "prefix2", chrome.Prefix2)
	restore(ctx, run, "status", chrome.Status)
	restore(ctx, run, "mouse", chrome.Mouse)
}

func show(ctx context.Context, run execx.Runner, name string) string {
	args := ShowOption(name)
	res := run(ctx, args[0], args[1:]...)
	return strings.TrimSpace(string(res.Stdout))
}

func set(ctx context.Context, run execx.Runner, name, value string) error {
	args := SetOption(name, value)
	res := run(ctx, args[0], args[1:]...)
	return res.Err
}

func restore(ctx context.Context, run execx.Runner, name, value string) {
	if value == "" {
		args := UnsetOption(name)
		_ = run(ctx, args[0], args[1:]...)
		return
	}
	_ = set(ctx, run, name, value)
}
