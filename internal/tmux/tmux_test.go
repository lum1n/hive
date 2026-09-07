package tmux

import (
	"strings"
	"testing"
)

func TestParseList(t *testing.T) {
	t.Parallel()
	raw := "backend\t3\t1\t1710000000\nagents\t1\t0\n\n"
	got := ParseList(raw)
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Name != "backend" || got[0].Windows != 3 || !got[0].Attached {
		t.Fatalf("first: %+v", got[0])
	}
	if got[1].Name != "agents" || got[1].Attached {
		t.Fatalf("second: %+v", got[1])
	}
}

func TestMissingServer(t *testing.T) {
	t.Parallel()
	if !MissingServer("error connecting to /tmp/tmux-1000/default: No such file") {
		t.Fatal("expected missing")
	}
	if MissingServer("Permission denied") {
		t.Fatal("auth is not missing")
	}
}

func TestArgs(t *testing.T) {
	t.Parallel()
	got := ListSessions("/opt/homebrew/bin/tmux", "mine")
	want0 := "/opt/homebrew/bin/tmux"
	if got[0] != want0 || got[1] != "-L" || got[2] != "mine" {
		t.Fatalf("%v", got)
	}
}

func TestSwitchClient(t *testing.T) {
	t.Parallel()
	got := SwitchClient("tmux", "", "backend")
	if strings.Join(got, " ") != "tmux switch-client -t =backend" {
		t.Fatalf("%v", got)
	}
}
