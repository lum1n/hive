package agents

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/lum1n/hive/internal/workspace"
)

const (
	Version           = 1
	MaxTargets        = 16
	MaxLines          = 500
	MaxBytes          = 65536
	MaxInventoryBytes = 4 << 20
)

var Kinds = []string{"copilot", "claude", "codex", "pi", "opencode", "cursor"}

var paneID = regexp.MustCompile(`^%[0-9]+$`)
var windowID = regexp.MustCompile(`^@[0-9]+$`)
var sessionID = regexp.MustCompile(`^\$[0-9]+$`)
var generationID = regexp.MustCompile(`^[1-9][0-9]*:[1-9][0-9]*$`)

type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (f *Failure) Error() string {
	return f.Message
}

func failure(code, message string) *Failure {
	return &Failure{Code: code, Message: message}
}

type Reference struct {
	Version    int    `json:"version"`
	Host       string `json:"host"`
	Socket     string `json:"socket"`
	Generation string `json:"generation"`
	Pane       string `json:"pane"`
}

func (r Reference) Validate() error {
	if r.Version != Version {
		return fmt.Errorf("unsupported agent reference version")
	}
	if err := workspace.ValidHostID(r.Host); err != nil {
		return fmt.Errorf("invalid agent host")
	}
	if !filepath.IsAbs(r.Socket) || filepath.Clean(r.Socket) != r.Socket ||
		len(r.Socket) > 1024 || strings.ContainsAny(r.Socket, "\x00\r\n") {
		return fmt.Errorf("invalid agent socket")
	}
	if !generationID.MatchString(r.Generation) || !paneID.MatchString(r.Pane) {
		return fmt.Errorf("invalid agent server or pane")
	}
	return nil
}

func (r Reference) ID() string {
	raw, _ := json.Marshal(r)
	return "hive-agent-v1." + base64.RawURLEncoding.EncodeToString(raw)
}

func ParseReference(id string) (Reference, error) {
	var r Reference
	const prefix = "hive-agent-v1."
	if !strings.HasPrefix(id, prefix) || len(id) > 4096 {
		return r, fmt.Errorf("invalid agent reference")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(id, prefix))
	if err != nil {
		return r, fmt.Errorf("invalid agent reference encoding")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&r); err != nil {
		return r, fmt.Errorf("invalid agent reference fields")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return r, fmt.Errorf("invalid trailing agent reference data")
	}
	return r, r.Validate()
}

type Membership struct {
	Session     string `json:"session"`
	SessionName string `json:"session_name"`
	Window      string `json:"window"`
	WindowName  string `json:"window_name"`
}

type Agent struct {
	ID         string       `json:"id"`
	Host       string       `json:"host"`
	Socket     string       `json:"socket"`
	Generation string       `json:"generation"`
	Pane       string       `json:"pane"`
	Window     string       `json:"window"`
	Session    string       `json:"session"`
	Kind       string       `json:"kind"`
	Path       string       `json:"path"`
	State      string       `json:"state"`
	Provenance string       `json:"provenance"`
	ObservedAt time.Time    `json:"observed_at"`
	Members    []Membership `json:"memberships"`
}

type Server struct {
	Socket     string   `json:"socket"`
	Generation string   `json:"generation,omitempty"`
	Status     string   `json:"status"`
	Error      *Failure `json:"error,omitempty"`
	Agents     []Agent  `json:"agents"`
}

type HostResult struct {
	Host    string   `json:"host"`
	Label   string   `json:"label"`
	Local   bool     `json:"local"`
	Status  string   `json:"status"`
	Error   *Failure `json:"error,omitempty"`
	Servers []Server `json:"servers"`
}

type ListResponse struct {
	Version int          `json:"version"`
	Time    time.Time    `json:"time"`
	Hosts   []HostResult `json:"hosts"`
}

type Capture struct {
	ID         string    `json:"id"`
	Text       string    `json:"text,omitempty"`
	Time       time.Time `json:"time"`
	State      string    `json:"state"`
	Provenance string    `json:"provenance"`
	Truncated  bool      `json:"truncated"`
	Error      *Failure  `json:"error,omitempty"`
}

type CaptureResponse struct {
	Version  int       `json:"version"`
	Captures []Capture `json:"captures"`
}

func knownKind(kind string) bool {
	for _, k := range Kinds {
		if kind == k {
			return true
		}
	}
	return false
}
