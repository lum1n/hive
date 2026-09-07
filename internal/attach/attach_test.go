package attach

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/execx"
	"github.com/lum1n/hive/internal/workspace"
)

func TestBackoff(t *testing.T) {
	t.Parallel()
	if Backoff(0) != time.Second {
		t.Fatal(Backoff(0))
	}
	if Backoff(1) != 2*time.Second {
		t.Fatal(Backoff(1))
	}
	if Backoff(10) != 30*time.Second {
		t.Fatal(Backoff(10))
	}
}

func TestIndexByte(t *testing.T) {
	t.Parallel()
	if indexByte([]byte("abc"), 0) != -1 {
		t.Fatal("missing")
	}
	if indexByte([]byte{'a', 0, 'b'}, 0) != 1 {
		t.Fatal("ctrl-space")
	}
}

func TestDropEnv(t *testing.T) {
	t.Parallel()
	got := dropEnv([]string{"TMUX=/tmp/tmux", "TERM=tmux-256color", "FOO=bar"}, "TMUX")
	if strings.Join(got, ",") != "TERM=tmux-256color,FOO=bar" {
		t.Fatalf("%v", got)
	}
}

func TestSessionSwitchClientInsideTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	var cmds []string
	run := func(ctx context.Context, name string, args ...string) execx.Result {
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "has-session"):
			return execx.Result{}
		case strings.Contains(joined, "switch-client"):
			return execx.Result{}
		case strings.Contains(joined, "display-message"):
			return execx.Result{Stdout: []byte("home\n")}
		default:
			t.Fatalf("unexpected %s %v", name, args)
			return execx.Result{}
		}
	}
	out, err := Session(context.Background(), Options{Runner: run}, config.Host{ID: "local", Local: true, Tmux: "tmux"}, workspace.ID{Host: "local", Session: "backend"})
	if err != nil {
		t.Fatal(err)
	}
	if out != OutcomeSwitched {
		t.Fatalf("outcome=%s", out)
	}
	joined := strings.Join(cmds, "\n")
	if !strings.Contains(joined, "switch-client -t =backend") {
		t.Fatalf("cmds:\n%s", joined)
	}
	if strings.Contains(joined, "attach-session") {
		t.Fatal("must not nest attach")
	}
}
