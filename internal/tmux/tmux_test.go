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

func TestParseListClassic(t *testing.T) {
	t.Parallel()
	raw := "13: 1 windows (created Sun Sep  7 10:00:00 2026)\nPRIV: 10 windows (created Mon Sep  1 09:00:00 2026) (attached)\n"
	got := ParseList(raw)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if got[0].Name != "13" || got[0].Windows != 1 || got[0].Attached {
		t.Fatalf("first: %+v", got[0])
	}
	if got[1].Name != "PRIV" || got[1].Windows != 10 || !got[1].Attached {
		t.Fatalf("second: %+v", got[1])
	}
}

func TestParseListUnderscore(t *testing.T) {
	t.Parallel()
	raw := "GAS_1_3_1788766403\ncavet_3_1_1788167850\nrepos/ai-command-center_4_0_1788770091\n"
	got := ParseList(raw)
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	if got[0].Name != "GAS" || got[0].Windows != 1 || !got[0].Attached {
		t.Fatalf("GAS: %+v", got[0])
	}
	if got[1].Name != "cavet" || got[1].Windows != 3 || !got[1].Attached {
		t.Fatalf("cavet: %+v", got[1])
	}
	if got[2].Name != "repos/ai-command-center" || got[2].Windows != 4 || got[2].Attached {
		t.Fatalf("repos: %+v", got[2])
	}
}

func TestParseListUnitSep(t *testing.T) {
	t.Parallel()
	raw := "backend" + ListSep + "3" + ListSep + "1" + ListSep + "1710000000\n"
	got := ParseList(raw)
	if len(got) != 1 || got[0].Name != "backend" || got[0].Windows != 3 || !got[0].Attached {
		t.Fatalf("%+v", got)
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

func TestArgsSocketPath(t *testing.T) {
	t.Parallel()
	got := Args("tmux", "/var/folders/xx/yy/T/tmux-501/default", "list-sessions")
	if strings.Join(got, " ") != "tmux -S /var/folders/xx/yy/T/tmux-501/default list-sessions" {
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
