package tmux

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/lum1n/hive/internal/cache"
	"github.com/lum1n/hive/internal/sshx"
)

const (
	ListSep    = "\x1f"
	ListFormat = "#{session_name}" + ListSep + "#{session_windows}" + ListSep + "#{session_attached}" + ListSep + "#{session_activity}"
)

func Args(bin, socket string, rest ...string) []string {
	if bin == "" {
		bin = "tmux"
	}
	args := []string{bin}
	if socket != "" {
		if strings.Contains(socket, "/") {
			args = append(args, "-S", socket)
		} else {
			args = append(args, "-L", socket)
		}
	}
	return append(args, rest...)
}

func ListSessions(bin, socket string) []string {
	return Args(bin, socket, "list-sessions", "-F", ListFormat)
}

func HasSession(bin, socket, name string) []string {
	return Args(bin, socket, "has-session", "-t", exact(name))
}

func NewSession(bin, socket, name string) []string {
	return Args(bin, socket, "new-session", "-d", "-s", name)
}

func KillSession(bin, socket, name string) []string {
	return Args(bin, socket, "kill-session", "-t", exact(name))
}

func RenameSession(bin, socket, from, to string) []string {
	return Args(bin, socket, "rename-session", "-t", exact(from), to)
}

// AttachShell is a login-free sh -c body: set options, then exec attach.
func AttachShell(bin, socket, name string) string {
	t := shellCmd(bin, socket)
	target := sshx.SingleQuote(exact(name))
	return fmt.Sprintf(
		"%s set-option -t %s window-size latest 2>/dev/null; "+
			"%s set-option -t %s destroy-unattached off 2>/dev/null; "+
			"exec %s -u attach-session -t %s",
		t, target, t, target, t, target,
	)
}

func AttachCommand(bin, socket, name string) []string {
	return []string{"sh", "-c", AttachShell(bin, socket, name)}
}

func shellCmd(bin, socket string) string {
	parts := Args(bin, socket)
	quoted := make([]string, len(parts))
	for i, p := range parts {
		quoted[i] = sshx.SingleQuote(p)
	}
	return strings.Join(quoted, " ")
}

func SwitchClient(bin, socket, name string) []string {
	return Args(bin, socket, "switch-client", "-t", exact(name))
}

func ClientName() []string {
	return []string{"tmux", "display-message", "-p", "#{client_name}"}
}

func PaneSession() []string {
	return []string{"tmux", "display-message", "-p", "#{session_name}"}
}

func ClientSession(client string) []string {
	return []string{"tmux", "display-message", "-c", client, "-p", "#{client_session}"}
}

func ShowOption(name string) []string {
	return []string{"tmux", "show-options", "-qv", name}
}

func SetOption(name, value string) []string {
	return []string{"tmux", "set-option", name, value}
}

func UnsetOption(name string) []string {
	return []string{"tmux", "set-option", "-u", name}
}

func Inside() bool {
	return os.Getenv("TMUX") != ""
}

func exact(name string) string {
	return "=" + name
}

func ParseList(raw string) []cache.Session {
	var out []cache.Session
	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if s, ok := parseLine(line); ok {
			out = append(out, s)
		}
	}
	return out
}

func parseLine(line string) (cache.Session, bool) {
	for _, sep := range []string{ListSep, "\t", "|"} {
		if s, ok := parseFields(strings.Split(line, sep)); ok {
			return s, true
		}
	}
	if s, ok := parseClassic(line); ok {
		return s, true
	}
	return parseUnderscore(line)
}

func parseFields(parts []string) (cache.Session, bool) {
	if len(parts) < 3 || parts[0] == "" {
		return cache.Session{}, false
	}
	windows, err := strconv.Atoi(parts[1])
	if err != nil {
		return cache.Session{}, false
	}
	activity := ""
	if len(parts) > 3 {
		activity = parts[3]
	}
	return cache.Session{
		Name:     parts[0],
		Windows:  windows,
		Attached: atoi(parts[2]) > 0,
		Activity: activity,
	}, true
}

func parseClassic(line string) (cache.Session, bool) {
	name, rest, ok := strings.Cut(line, ": ")
	if !ok || name == "" {
		return cache.Session{}, false
	}
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return cache.Session{}, false
	}
	if fields[1] != "windows" && fields[1] != "window" {
		return cache.Session{}, false
	}
	windows, err := strconv.Atoi(fields[0])
	if err != nil {
		return cache.Session{}, false
	}
	return cache.Session{
		Name:     name,
		Windows:  windows,
		Attached: strings.Contains(line, "(attached)"),
	}, true
}

func parseUnderscore(line string) (cache.Session, bool) {
	name, activity, ok := cutTrailingInt(line)
	if !ok {
		return cache.Session{}, false
	}
	name, attached, ok := cutTrailingInt(name)
	if !ok {
		return cache.Session{}, false
	}
	name, windows, ok := cutTrailingInt(name)
	if !ok || name == "" {
		return cache.Session{}, false
	}
	return cache.Session{
		Name:     name,
		Windows:  windows,
		Attached: attached > 0,
		Activity: strconv.Itoa(activity),
	}, true
}

func cutTrailingInt(s string) (string, int, bool) {
	i := strings.LastIndexByte(s, '_')
	if i <= 0 || i == len(s)-1 {
		return "", 0, false
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil {
		return "", 0, false
	}
	return s[:i], n, true
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func MissingServer(message string) bool {
	for _, part := range []string{
		"no server running",
		"no sessions",
		"can't find session",
		"error connecting to ",
	} {
		if strings.Contains(message, part) {
			return true
		}
	}
	return false
}
