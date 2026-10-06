package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lum1n/hive/internal/agents"
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
		{}, {"unknown"}, {"list"}, {"capture", "--json"}, {"capture", "--json", "--id", "invalid"},
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
	for _, command := range []string{"list", "capture"} {
		args := []string{command, "--json"}
		if command == "capture" {
			args = append(args, "--id", ref.ID())
		}
		var out, diagnostics bytes.Buffer
		err := Run(context.Background(), args, path, &out, &diagnostics)
		var exit *ExitError
		if !errors.As(err, &exit) || exit.Code != 2 {
			t.Fatal("operational failure did not return exit 2")
		}
		if command == "list" {
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
