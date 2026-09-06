package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lum1n/hive/internal/workspace"
)

type Status string

const (
	StatusConnecting Status = "connecting"
	StatusOnline     Status = "online"
	StatusOffline    Status = "offline"
	StatusAuth       Status = "auth"
)

type Session struct {
	Name     string `json:"name"`
	Windows  int    `json:"windows"`
	Attached bool   `json:"attached"`
	Activity string `json:"activity,omitempty"`
}

type HostSnapshot struct {
	Time     time.Time `json:"time"`
	Status   Status    `json:"status"`
	Error    string    `json:"error,omitempty"`
	Sessions []Session `json:"sessions"`
}

type lastFile struct {
	URI string `json:"uri"`
}

type Store struct {
	dir string
	mu  sync.Mutex
}

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("state dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

func DefaultDir() string {
	if p := os.Getenv("HIVE_STATE_DIR"); p != "" {
		return p
	}
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "hive")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "hive-state")
	}
	return filepath.Join(home, ".local", "state", "hive")
}

func (s *Store) LoadHost(id string) HostSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.hostPath(id))
	if err != nil {
		return HostSnapshot{Status: StatusConnecting}
	}
	var snap HostSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return HostSnapshot{Status: StatusConnecting}
	}
	return snap
}

func (s *Store) SaveHost(id string, snap HostSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeAtomic(s.hostPath(id), snap)
}

func (s *Store) LastWorkspace() (workspace.ID, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.lastPath())
	if err != nil {
		return workspace.ID{}, false
	}
	var last lastFile
	if err := json.Unmarshal(raw, &last); err != nil || last.URI == "" {
		return workspace.ID{}, false
	}
	id, err := workspace.Parse(last.URI)
	if err != nil {
		return workspace.ID{}, false
	}
	return id, true
}

func (s *Store) SetLastWorkspace(id workspace.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeAtomic(s.lastPath(), lastFile{URI: id.String()})
}

func (s *Store) hostPath(id string) string {
	return filepath.Join(s.dir, "hosts", id+".json")
}

func (s *Store) lastPath() string {
	return filepath.Join(s.dir, "last.json")
}

func writeAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
