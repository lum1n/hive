package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lum1n/hive/internal/prefix"
	"github.com/lum1n/hive/internal/workspace"
	"github.com/pelletier/go-toml/v2"
)

const DefaultPrefix = "ctrl-space"

type Config struct {
	Prefix         string `toml:"prefix"`
	ControlPersist int    `toml:"control_persist"`
	Hosts          []Host `toml:"hosts"`
}

type Host struct {
	ID          string `toml:"id"`
	SSH         string `toml:"ssh"`
	Label       string `toml:"label"`
	Tmux        string `toml:"tmux"`
	Socket      string `toml:"socket"`
	ControlPath string `toml:"control_path"`
	Local       bool   `toml:"local"`
}

func (h Host) Display() string {
	if h.Label != "" {
		return h.Label
	}
	return h.ID
}

func (h Host) TmuxBin() string {
	if h.Tmux != "" {
		return h.Tmux
	}
	return "tmux"
}

func (h Host) Destination() string {
	if h.SSH != "" {
		return h.SSH
	}
	return h.ID
}

func (c Config) Host(id string) (Host, bool) {
	for _, h := range c.Hosts {
		if h.ID == id {
			return h, true
		}
	}
	return Host{}, false
}

func (c Config) PrefixByte() (byte, error) {
	return prefix.Parse(c.Prefix)
}

func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := cfg.normalize(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) normalize() error {
	if strings.TrimSpace(c.Prefix) == "" {
		c.Prefix = DefaultPrefix
	}
	if c.ControlPersist <= 0 {
		c.ControlPersist = 120
	}
	if len(c.Hosts) == 0 {
		return fmt.Errorf("config has no hosts")
	}
	seen := make(map[string]struct{}, len(c.Hosts))
	for i := range c.Hosts {
		h := &c.Hosts[i]
		h.ID = strings.TrimSpace(h.ID)
		h.SSH = strings.TrimSpace(h.SSH)
		h.Label = strings.TrimSpace(h.Label)
		h.Tmux = strings.TrimSpace(h.Tmux)
		h.Socket = strings.TrimSpace(h.Socket)
		h.ControlPath = strings.TrimSpace(h.ControlPath)
		if err := workspace.ValidHostID(h.ID); err != nil {
			return err
		}
		if _, ok := seen[h.ID]; ok {
			return fmt.Errorf("duplicate host id %q", h.ID)
		}
		seen[h.ID] = struct{}{}
		if !h.Local && h.SSH == "" {
			h.SSH = h.ID
		}
		if h.Local && h.SSH != "" {
			return fmt.Errorf("host %q: set local or ssh, not both", h.ID)
		}
	}
	if _, err := c.PrefixByte(); err != nil {
		return err
	}
	return nil
}

func DefaultPath() string {
	if p := os.Getenv("HIVE_CONFIG"); p != "" {
		return p
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "hive", "hive.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "hive.toml"
	}
	return filepath.Join(home, ".config", "hive", "hive.toml")
}

const Example = `# Hive workspace hosts. Opt-in only.

prefix = "ctrl-space"

[[hosts]]
id = "local"
label = "local"
local = true
tmux = "tmux"

# [[hosts]]
# id = "devbox"
# ssh = "devbox"
# label = "devbox"
# tmux = "tmux"
`

func WriteExample(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	return os.WriteFile(path, []byte(Example), 0o644)
}
