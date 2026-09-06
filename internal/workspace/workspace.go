package workspace

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// ID is a stable workspace address: tmux://<machine>/<session>.
type ID struct {
	Host    string
	Session string
}

func (id ID) String() string {
	if id.Host == "" && id.Session == "" {
		return ""
	}
	return "tmux://" + id.Host + "/" + id.Session
}

func (id ID) Display() string {
	if id.Host == "" {
		return id.Session
	}
	return id.Host + "/" + id.Session
}

func (id ID) Empty() bool {
	return id.Host == "" || id.Session == ""
}

// Parse accepts tmux://host/session or host/session.
func Parse(raw string) (ID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ID{}, fmt.Errorf("empty workspace")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return ID{}, fmt.Errorf("workspace %q: %w", raw, err)
		}
		if u.Scheme != "tmux" {
			return ID{}, fmt.Errorf("workspace %q: unsupported scheme %q", raw, u.Scheme)
		}
		host := u.Host
		if host == "" {
			host = u.Hostname()
		}
		session := strings.TrimPrefix(u.Path, "/")
		if host == "" || session == "" {
			return ID{}, fmt.Errorf("workspace %q: need tmux://host/session", raw)
		}
		if err := ValidHostID(host); err != nil {
			return ID{}, err
		}
		return ID{Host: host, Session: session}, nil
	}
	host, session, ok := strings.Cut(raw, "/")
	if !ok || host == "" || session == "" {
		return ID{}, fmt.Errorf("workspace %q: need host/session", raw)
	}
	if err := ValidHostID(host); err != nil {
		return ID{}, err
	}
	return ID{Host: host, Session: session}, nil
}

func ValidHostID(id string) error {
	if id == "" {
		return fmt.Errorf("empty host id")
	}
	if len(id) > 64 {
		return fmt.Errorf("host id %q is too long", id)
	}
	for i, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		if i > 0 && (r == '_' || r == '-' || r == '.') {
			continue
		}
		return fmt.Errorf("host id %q must match [A-Za-z0-9][A-Za-z0-9_.-]*", id)
	}
	return nil
}
