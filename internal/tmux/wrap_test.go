package tmux

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPreludeFindsMacSockets(t *testing.T) {
	t.Parallel()
	p := Prelude("tmux", "")
	for _, want := range []string{
		"/opt/homebrew/bin",
		"/opt/homebrew/opt/tmux/bin",
		"/opt/homebrew/Cellar/tmux/",
		"brew --prefix",
		"/usr/local/opt/tmux/bin",
		"/opt/local/bin",
		"/sw/bin",
		"/home/linuxbrew/.linuxbrew/bin",
		"$HOME/.linuxbrew/bin",
		"$HOME/.nix-profile/bin",
		"$HOME/.asdf/shims",
		"$HOME/miniconda3/bin",
		"/private/tmp/tmux-",
		"/var/folders",
		"hive_add_sock",
		"HIVE_TMUX_SOCKS",
		"unset TMUX",
		"lsof",
		"hive_tmux()",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestPreludeResolvesBareTmux(t *testing.T) {
	t.Parallel()
	p := Prelude("tmux", "")
	if !strings.Contains(p, `lsof -nP -c tmux`) {
		t.Fatal("must prefer the live tmux binary")
	}
	if !strings.Contains(p, "/proc/") {
		t.Fatal("linux live binary via /proc/pid/exe")
	}
	if strings.Contains(p, "HIVE_TMUX_BIN='tmux'") {
		t.Fatal("bare tmux must search, not pin the name")
	}
}

func TestPreludeExplicitSocket(t *testing.T) {
	t.Parallel()
	p := Prelude("/opt/homebrew/bin/tmux", "mine")
	if !strings.Contains(p, "HIVE_TMUX_L='mine'") {
		t.Fatalf("%s", p)
	}
	if !strings.Contains(p, "HIVE_TMUX_BIN='/opt/homebrew/bin/tmux'") {
		t.Fatalf("%s", p)
	}
}

func TestCmdUsesShellWhenNotInsideTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	got := Cmd(false, "tmux", "", "list-sessions")
	if got[0] != "sh" || got[1] != "-c" {
		t.Fatalf("%v", got)
	}
	if !strings.Contains(got[2], "list-sessions") {
		t.Fatalf("%s", got[2])
	}
}

func TestCmdDirectInsideTmuxLocal(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	got := Cmd(true, "tmux", "", "list-sessions", "-F", "x")
	if strings.Join(got, " ") != "tmux list-sessions -F x" {
		t.Fatalf("%v", got)
	}
}

func TestPreludeProbesSocketsInsteadOfFirstFile(t *testing.T) {
	t.Parallel()
	p := Prelude("tmux", "")
	if strings.Contains(p, `if [ -z "${TMUX:-}" ]`) {
		t.Fatal("must not skip socket scan when $TMUX is set")
	}
	if !strings.Contains(p, "list-sessions >/dev/null") {
		t.Fatal("must probe sockets; a dead /tmp socket hid the GUI server")
	}
}

func TestWrapScriptSyntax(t *testing.T) {
	t.Parallel()
	script := Script("tmux", "", "list-sessions", "-F", "x")
	cmd := exec.Command("sh", "-n", "-c", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v\n%s", err, out)
	}
}

func TestCmdWrapsRemoteEvenInsideLocalTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	got := Cmd(false, "tmux", "", "list-sessions")
	if got[0] != "sh" || got[1] != "-c" {
		t.Fatalf("ssh hosts must wrap despite local $TMUX: %v", got)
	}
}
