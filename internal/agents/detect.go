package agents

import (
	"path"
	"regexp"
	"strings"
	"unicode"
)

var escapes = regexp.MustCompile("\x1b(?:\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|[P_^][\\s\\S]*?\x1b\\\\|\\[[0-?]*[ -/]*[@-~]|.)")

func Clean(text string) string {
	text = escapes.ReplaceAllString(strings.ToValidUTF8(text, "�"), "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, text)
}

func display(text string) string {
	return strings.Join(strings.Fields(Clean(text)), " ")
}

func Detect(command string) string {
	switch strings.ToLower(path.Base(command)) {
	case "copilot", "claude", "codex", "pi", "opencode":
		return strings.ToLower(path.Base(command))
	case "cursor-agent":
		return "cursor"
	}
	for _, p := range []struct{ kind, marker string }{
		{"copilot", "@github/copilot/"}, {"claude", "@anthropic-ai/claude-code/"},
		{"codex", "@openai/codex/"}, {"pi", "pi-coding-agent/"},
		{"opencode", "opencode-ai/"}, {"cursor", "/cursor-agent/"},
	} {
		if strings.Contains(command, p.marker) {
			return p.kind
		}
	}
	return ""
}

type process struct {
	parent  int
	command string
	args    string
}

func descendantKind(root int, processes map[int]process) string {
	children := make(map[int][]int)
	for pid, p := range processes {
		children[p.parent] = append(children[p.parent], pid)
	}
	pending, seen, kinds := []int{root}, map[int]bool{}, map[string]bool{}
	for len(pending) > 0 && len(seen) < 1024 {
		pid := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		p := processes[pid]
		kind := Detect(p.command)
		if kind == "" {
			switch path.Base(p.command) {
			case "node", "bun", "deno":
				kind = Detect(p.args)
			}
		}
		if kind != "" {
			kinds[kind] = true
		}
		pending = append(pending, children[pid]...)
	}
	if len(pending) != 0 || len(kinds) != 1 {
		return ""
	}
	for kind := range kinds {
		return kind
	}
	return ""
}

var idlePrompt = regexp.MustCompile(`^(?:❯|>|›)\s*$`)
var working = regexp.MustCompile(`(?:esc to (?:interrupt|cancel)|thinking(?:\.|…)|working(?:\.|…)|generating(?:\.|…))`)
var permission = regexp.MustCompile(`(?:do you (?:want to|trust)|allow (?:once|always|this)|permission required|\[y/n\]|approve this)`)
var errored = regexp.MustCompile(`(?:^|\n)(?:error:|fatal:|authentication failed|rate limit exceeded)`)
var tool = regexp.MustCompile(`(?:running tool|executing tool|running command)`)

func Classify(text string) string {
	var lines []string
	for line := range strings.SplitSeq(Clean(text), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return "unknown"
	}
	tail := strings.ToLower(strings.Join(lines[max(0, len(lines)-4):], "\n"))
	if idlePrompt.MatchString(lines[len(lines)-1]) && !strings.Contains(tail, "esc to interrupt") && !strings.Contains(tail, "esc to cancel") {
		return "idle"
	}
	switch {
	case permission.MatchString(tail):
		return "waiting-permission"
	case errored.MatchString(tail):
		return "errored"
	case working.MatchString(tail):
		return "thinking"
	case tool.MatchString(tail):
		return "running-tool"
	}
	return "unknown"
}
