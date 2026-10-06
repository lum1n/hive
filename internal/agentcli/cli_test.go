package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lum1n/hive/internal/agents"
	"golang.org/x/term"
)

func TestCapabilitiesWithoutConfigOrTerminal(t *testing.T) {
	var out, diagnostics bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing.toml")
	if err := Run(context.Background(), []string{"capabilities", "--json"}, missing, &out, &diagnostics); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Version != 1 || diagnostics.Len() != 0 {
		t.Fatal("capabilities are not clean versioned JSON")
	}
}

func TestInvalidArgumentsBeforeHostAccess(t *testing.T) {
	for _, args := range [][]string{
		{"unknown"}, {"list"}, {"capture", "--json"}, {"capture", "--json", "--id", "invalid"},
		{"attach", "--json", "--id", "invalid"}, {"list", "--json", "--timeout", "0s"},
		{"capabilities", "--json", "extra"}, {"capture", "--json", "--lines", "501"},
	} {
		var out, diagnostics bytes.Buffer
		if err := Run(context.Background(), args, "/missing-fixture.toml", &out, &diagnostics); err == nil {
			t.Fatalf("accepted invalid args %v", args)
		}
		if out.Len() != 0 {
			t.Fatal("invalid arguments wrote success-shaped output")
		}
	}

}

func TestOperationalFailuresKeepCompleteJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hive.toml")
	socket := filepath.Join(dir, "missing.sock")
	raw := fmt.Sprintf("[[hosts]]\nid = \"fixture\"\nlocal = true\ntmux = %q\nsocket = %q\n",
		filepath.Join(dir, "missing-tmux"), socket)
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := agents.Reference{Version: 1, Host: "fixture", Socket: socket, Generation: "1:1", Pane: "%0"}
	for _, command := range []string{"list", "capture", "group"} {
		args := []string{command, "--json"}
		if command == "group" {
			args = []string{"--json"}
		}
		if command == "capture" {
			args = append(args, "--id", ref.ID())
		}
		var out, diagnostics bytes.Buffer
		err := Run(context.Background(), args, path, &out, &diagnostics)
		var exit *ExitError
		if !errors.As(err, &exit) || exit.Code != 2 {
			t.Fatal("operational failure did not return exit 2")
		}
		if command != "capture" {
			var response agents.ListResponse
			if err := json.Unmarshal(out.Bytes(), &response); err != nil ||
				response.Version != 1 || len(response.Hosts) != 1 || response.Hosts[0].Error == nil ||
				response.Hosts[0].Status != "unavailable" || response.Hosts[0].Servers == nil {
				t.Fatal("host failure lost the complete versioned response")
			}
		} else {
			var response agents.CaptureResponse
			if err := json.Unmarshal(out.Bytes(), &response); err != nil ||
				response.Version != 1 || len(response.Captures) != 1 || response.Captures[0].Error == nil ||
				response.Captures[0].ID != ref.ID() || response.Captures[0].Text != "" ||
				response.Captures[0].State != "unknown" || response.Captures[0].Provenance != "unavailable" {
				t.Fatal("capture failure lost the target or reported healthy content")
			}
		}
	}

}

func TestHelpWithoutConfigurationOrTerminal(t *testing.T) {
	cases := [][]string{{"--help"}, {"-h"}}
	for _, command := range []string{"capabilities", "list", "capture", "attach"} {
		cases = append(cases, []string{command, "--help"}, []string{command, "-h"})
	}
	for _, args := range cases {
		var out, help bytes.Buffer
		path := filepath.Join(t.TempDir(), "not-created", "hive.toml")
		if err := Run(context.Background(), args, path, &out, &help); err != nil {
			t.Fatalf("help failed for %v: %v", args, err)
		}
		if out.Len() != 0 || !bytes.Contains(help.Bytes(), []byte("Usage: hive agents")) ||
			!bytes.Contains(help.Bytes(), []byte("Examples:")) {
			t.Fatalf("incomplete help for %v", args)
		}
		if len(args) == 2 && (!bytes.Contains(help.Bytes(), []byte("-config")) ||
			!bytes.Contains(help.Bytes(), []byte("-timeout"))) {
			t.Fatal("subcommand help omitted common flags")
		}
		if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
			t.Fatal("help created configuration resources")
		}
	}
	for _, args := range [][]string{{"--help"}, {"capture", "--help"}} {
		if err := Run(context.Background(), args, "", io.Discard, failedHelpWriter{}); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal("help swallowed an output failure")
		}
	}
}

type failedHelpWriter struct{}

func (failedHelpWriter) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }

func TestInteractiveAgentsRequireTerminalBeforeStartup(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
		t.Skip("this test requires redirected input or output")
	}
	for _, args := range [][]string{nil, {"list"}, {"--host", "fixture"}} {
		var out, diagnostics bytes.Buffer
		err := Run(context.Background(), args, filepath.Join(t.TempDir(), "missing.toml"), &out, &diagnostics)
		if err == nil || !strings.Contains(err.Error(), "needs a terminal") || out.Len() != 0 {
			t.Fatal("interactive agent picker started without a terminal")
		}
	}
}
