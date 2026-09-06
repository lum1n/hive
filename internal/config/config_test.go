package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hive.toml")
	raw := `
prefix = "ctrl-g"

[[hosts]]
id = "local"
local = true

[[hosts]]
id = "devbox"
ssh = "devbox"
label = "box"
`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Prefix != "ctrl-g" || cfg.ControlPersist != 120 {
		t.Fatalf("%+v", cfg)
	}
	if len(cfg.Hosts) != 2 {
		t.Fatalf("hosts=%d", len(cfg.Hosts))
	}
	h, ok := cfg.Host("devbox")
	if !ok || h.Display() != "box" || h.Destination() != "devbox" {
		t.Fatalf("devbox: %+v", h)
	}
}

func TestRejectDuplicateAndMixed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "hive.toml")
	if err := os.WriteFile(path, []byte(`
[[hosts]]
id = "x"
local = true
ssh = "x"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidPrefix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "hive.toml")
	if err := os.WriteFile(path, []byte(`
prefix = "nope"
[[hosts]]
id = "local"
local = true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected prefix error")
	}
}
