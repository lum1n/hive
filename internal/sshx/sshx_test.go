package sshx

import (
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
