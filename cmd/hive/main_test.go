package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHelpAndCapabilitiesWithoutStartup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("HIVE_CONFIG", filepath.Join(dir, "hive.toml"))
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"agents", "--help"},
		{"agents", "list", "--help"}, {"agents", "attach", "-h"},
	} {
		var out, help bytes.Buffer
		if err := runArgs(args, &out, &help); err != nil {
			t.Fatalf("help failed for %v: %v", args, err)
		}
		if out.Len() != 0 || !bytes.Contains(help.Bytes(), []byte("Usage: hive")) ||
			!bytes.Contains(help.Bytes(), []byte("Examples:")) {
			t.Fatalf("incomplete help for %v", args)
		}
		if len(args) == 1 && args[0] != "agents" && !bytes.Contains(help.Bytes(), []byte("hive agents --help")) {
			t.Fatal("root help omitted agent commands")
		}
	}
	var out, diagnostics bytes.Buffer
	if err := runArgs([]string{"agents", "capabilities", "--json"}, &out, &diagnostics); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Version != 1 || diagnostics.Len() != 0 {
		t.Fatal("capabilities startup changed")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("help or capabilities created configuration resources")
	}
}
