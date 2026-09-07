package sshx

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestExecArgs(t *testing.T) {
	t.Parallel()
	args := ExecArgs(Options{RuntimeDir: "/tmp/hive", ControlPersist: 120 * time.Second}, "devbox", "devbox", "", []string{"tmux", "list-sessions"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "BatchMode=yes") {
		t.Fatalf("missing BatchMode: %s", joined)
	}
	if !strings.Contains(joined, "ControlMaster=auto") {
		t.Fatalf("missing mux: %s", joined)
	}
	if !strings.Contains(joined, "'tmux' 'list-sessions'") {
		t.Fatalf("remote: %s", joined)
	}
}

func TestControlPathReuse(t *testing.T) {
	t.Parallel()
	args := AttachArgs(Options{RuntimeDir: "/tmp/hive"}, "laptop", "user@laptop", "~/.ssh/hive.sock", []string{"sh", "-c", "true"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "ProxyCommand=false") {
		t.Fatalf("%s", joined)
	}
	if strings.Contains(joined, "ControlMaster=auto") {
		t.Fatalf("should use existing master: %s", joined)
	}
}

func TestSingleQuote(t *testing.T) {
	t.Parallel()
	if SingleQuote("a'b") != `'a'"'"'b'` {
		t.Fatal(SingleQuote("a'b"))
	}
}

func TestDecodePipeRuns(t *testing.T) {
	t.Parallel()
	out, err := exec.Command("sh", "-c", decodePipe("echo ok")).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("%v %s", err, out)
	}
}

func TestRemoteCommandBase64ForShell(t *testing.T) {
	t.Parallel()
	got := remoteCommand([]string{"sh", "-c", "echo hive_ok"})
	if !strings.Contains(got, "base64") || !strings.Contains(got, "mktemp") || strings.Contains(got, "echo hive_ok") {
		t.Fatalf("expected base64 temp script, got %s", got)
	}
}

func TestAttachArgsForcesTTY(t *testing.T) {
	t.Parallel()
	args := AttachArgs(Options{RuntimeDir: "/tmp/hive"}, "box", "box", "", []string{"sh", "-c", "true"})
	joined := strings.Join(args, " ")
	if args[0] != "-tt" {
		t.Fatalf("%v", args)
	}
	if !strings.Contains(joined, "ControlPath=none") || !strings.Contains(joined, "RequestTTY=force") {
		t.Fatalf("attach must not reuse the listing mux: %s", joined)
	}
	if strings.Contains(joined, "ControlMaster=auto") {
		t.Fatalf("%s", joined)
	}
}
