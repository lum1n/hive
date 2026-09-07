package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lum1n/hive/internal/sshx"
)

func TestPreludeFindsMacSockets(t *testing.T) {
	t.Parallel()
	p := Prelude("tmux", "")
	for _, want := range []string{
		"/opt/homebrew/bin",
		"/opt/homebrew/opt/tmux/bin",
		"/opt/homebrew/Cellar/tmux/",
		"/usr/local/opt/tmux/bin",
		"/opt/local/bin",
		"/sw/bin",
		"/home/linuxbrew/.linuxbrew/bin",
		"$HOME/.linuxbrew/bin",
		"$HOME/.nix-profile/bin",
		"$HOME/.asdf/shims",
		"$HOME/miniconda3/bin",
		"/usr/sbin",
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

func TestPreludePrefersLiveSocketsOverTmp(t *testing.T) {
	t.Parallel()
	p := Prelude("tmux", "")
	if strings.Contains(p, `if [ -z "${TMUX:-}" ]`) {
		t.Fatal("must not skip socket scan when $TMUX is set")
	}
	lsofAt := strings.Index(p, `-U -Fn`)
	tmpAt := strings.Index(p, "/tmp/tmux-$uid/*")
	if lsofAt < 0 || tmpAt < 0 || lsofAt > tmpAt {
		t.Fatal("lsof live sockets must be collected before /tmp files")
	}
	if strings.Contains(p, "list-sessions >/dev/null") {
		t.Fatal("do not probe sockets with list-sessions")
	}
}

func TestWrapScriptSyntax(t *testing.T) {
	t.Parallel()
	script := Script("tmux", "", "list-sessions", "-F", "x")
	cmd := exec.Command("sh", "-n", "-c", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v\n%s", err, out)
	}
	attach := AttachScript("tmux", "", "cavet")
	cmd = exec.Command("sh", "-n", "-c", attach)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("attach sh -n: %v\n%s", err, out)
	}
}

func TestHiveTmuxExecKeepsCallerStdin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	body := "#!/bin/sh\n" +
		`if [ "$1" = "-S" ]; then shift 2; fi` + "\n" +
		`if [ "$1" = "has-session" ]; then exit 0; fi` + "\n" +
		"cat\n"
	if err := os.WriteFile(fake, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	script := tmuxFn +
		"HIVE_TMUX_BIN=" + sshx.SingleQuote(fake) + "\n" +
		"HIVE_TMUX_SOCK=\nHIVE_TMUX_L=\n" +
		"HIVE_TMUX_SOCKS='/sock-a\n/sock-b'\n" +
		"hive_tmux_exec -u attach-session -t =cavet\n"
	cmd := exec.Command("sh", "-c", script)
	cmd.Stdin = strings.NewReader("MARKER\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "MARKER") {
		t.Fatalf("attach stdin must stay the caller stream, got %q", got)
	}
	if strings.Contains(got, "/sock-b") {
		t.Fatalf("attach inherited the socket heredoc: %q", got)
	}
}

func TestWrapListsLiveServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("TMUX", "")
	cmd := exec.Command("tmux", "list-sessions")
	if err := cmd.Run(); err != nil {
		t.Skip("no live tmux server")
	}
	script := Script("tmux", "", "list-sessions", "-F", ListFormat)
	out, err := exec.Command("sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("wrap: %v\n%s", err, out)
	}
	got := ParseList(string(out))
	if len(got) == 0 {
		t.Fatalf("wrap listed nothing\n%s", out)
	}
}

func TestWrapThroughZshLikeSSH(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not installed")
	}
	t.Setenv("TMUX", "")
	if err := exec.Command("tmux", "list-sessions").Run(); err != nil {
		t.Skip("no live tmux server")
	}
	remote := []string{"sh", "-c", Script("tmux", "", "list-sessions", "-F", ListFormat)}
	quoted := make([]string, len(remote))
	for i, a := range remote {
		quoted[i] = sshx.SingleQuote(a)
	}
	cmdline := strings.Join(quoted, " ")
	out, err := exec.Command("zsh", "-c", cmdline).CombinedOutput()
	if err != nil {
		t.Fatalf("zsh -c wrap: %v\n%s", err, out)
	}
	got := ParseList(string(out))
	if len(got) == 0 {
		t.Fatalf("zsh wrap listed nothing\n%s", out)
	}
}

func TestCmdWrapsRemoteEvenInsideLocalTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	got := Cmd(false, "tmux", "", "list-sessions")
	if got[0] != "sh" || got[1] != "-c" {
		t.Fatalf("ssh hosts must wrap despite local $TMUX: %v", got)
	}
}
