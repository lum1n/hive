package agents

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

func testReference(host, socket string) Reference {
	return Reference{Version: Version, Host: host, Socket: socket, Generation: "123:1700000000", Pane: "%1"}
}

func b64(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

func TestReferences(t *testing.T) {
	ref := testReference("devbox", "/socket with spaces/tmux.sock")
	got, err := ParseReference(ref.ID())
	if err != nil || got != ref {
		t.Fatalf("round trip: %v", err)
	}
	for _, other := range []Reference{
		testReference("other", ref.Socket), testReference(ref.Host, "/other.sock"),
		{Version: 1, Host: ref.Host, Socket: ref.Socket, Generation: "456:1700000000", Pane: ref.Pane},
	} {
		if other.ID() == ref.ID() {
			t.Fatal("reference collision")
		}
	}
	for _, raw := range []string{"", "garbage", ref.ID() + "!",
		"hive-agent-v1." + base64.RawURLEncoding.EncodeToString([]byte(`{"version":1,"host":"x","socket":"/a","generation":"1:2","pane":"%0","extra":true}`)),
		"hive-agent-v1." + base64.RawURLEncoding.EncodeToString([]byte(`{} {}`))} {
		if _, err := ParseReference(raw); err == nil {
			t.Fatal("accepted invalid reference")
		}
	}
	for _, socket := range []string{"relative", "/a/../b", "/a\nb", "/a\x00b"} {
		bad := ref
		bad.Socket = socket
		if err := bad.Validate(); err == nil {
			t.Fatal("accepted invalid socket")
		}
	}
}

func TestDetectionAndDescendants(t *testing.T) {
	for _, kind := range Kinds {
		command := kind
		if kind == "cursor" {
			command = "cursor-agent"
		}
		if Detect("/bin/"+command) != kind {
			t.Fatalf("missing %s", kind)
		}
	}
	for command, kind := range map[string]string{
		"node /x/@github/copilot/index.js":         "copilot",
		"node /x/@anthropic-ai/claude-code/cli.js": "claude",
		"bun /x/@openai/codex/main.js":             "codex",
		"node /x/pi-coding-agent/index.js":         "pi",
		"bun /x/opencode-ai/index.js":              "opencode",
		"/x/cursor-agent/index.js":                 "cursor",
		"node":                                     "", "python": "", "agent": "", "echo copilot": "", "not-claude": "",
	} {
		if Detect(command) != kind {
			t.Fatalf("wrong detection for synthetic fixture %q", command)
		}
	}
	tree := map[int]process{1: {command: "sh"}, 2: {parent: 1, command: "node", args: "node /x/@github/copilot/index.js"},
		3: {parent: 2, command: "git"}}
	if descendantKind(1, tree) != "copilot" {
		t.Fatal("wrapper descendant not detected")
	}
	tree[4] = process{parent: 1, command: "claude"}
	if descendantKind(1, tree) != "" {
		t.Fatal("ambiguous descendants were attributed")
	}
}

func TestSanitizationAndStates(t *testing.T) {
	if Clean("\x1b]52;c;clipboard\x07hello\x1b[31m!\x00\x1b[0m") != "hello!" {
		t.Fatal("unsafe escapes survived")
	}
	for text, state := range map[string]string{
		"inconclusive": "unknown", ">": "idle", "Working... esc to interrupt": "thinking",
		"running tool": "running-tool", "Do you want to allow this action?": "waiting-permission",
		"Error: request failed": "errored", "old permission required\ncompleted\n>": "idle",
	} {
		if Classify(text) != state {
			t.Fatalf("incorrect synthetic state %s", state)
		}
	}
}

func inventoryFixture() string {
	return "V\t1\nP\t" + b64("1 0 sh\n2 1 node") + "\nA\t2\t" + b64("node /x/@github/copilot/index.js") +
		"\nS\t" + b64("/fixture.sock") + "\t123:1700000000\n" + paneFixture("%1", "$1", "", "0", "0", "shell\tname\n", "node")
}

func paneFixture(pane, session, override, owned, dead, name, command string) string {
	return strings.Join([]string{"N", pane, "@1", session, "1", "1", dead, owned,
		b64(override), b64(command), b64("/project"), b64(name), b64("agents")}, "\t") + "\n"
}

func TestInventoryLinkedDedupAndExclusions(t *testing.T) {
	raw := inventoryFixture() + paneFixture("%1", "$2", "", "0", "0", "linked", "node") +
		paneFixture("%2", "$1", "off", "0", "0", "shell", "copilot") +
		paneFixture("%3", "$1", "claude", "1", "0", "overview", "claude") +
		paneFixture("%4", "$1", "claude", "0", "1", "dead", "claude")
	servers, err := parseInventory([]byte(raw), "devbox", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || len(servers[0].Agents) != 1 {
		t.Fatal("wrong inventory count")
	}
	agent := servers[0].Agents[0]
	if agent.Kind != "copilot" || len(agent.Members) != 2 || agent.Members[0].SessionName != "shell name" {
		t.Fatal("wrong memberships or wrapper attribution")
	}
	for _, invalid := range []string{"", "V\t2\n", "V\t1\n", inventoryFixture() + "unexpected\n",
		strings.Replace(inventoryFixture(), "%1", "not-a-pane", 1)} {
		if _, err := parseInventory([]byte(invalid), "devbox", time.Now()); err == nil {
			t.Fatal("accepted malformed inventory")
		}
	}
}

func TestCaptureProtocol(t *testing.T) {
	refs := []Reference{testReference("devbox", "/fixture.sock"), testReference("devbox", "/other.sock")}
	raw := "V\t1\nP\t" + b64("1 0 copilot") + "\nC\t0\t0\t1\t\t" + b64("copilot") +
		"\t" + b64("\x1b]52;c;ignored\x07Working... esc to interrupt") + "\nF\t1\t8\n"
	captures, err := parseCaptures([]byte(raw), refs)
	if err != nil {
		t.Fatal(err)
	}
	if captures[0].State != "thinking" || strings.Contains(captures[0].Text, "\x1b") ||
		captures[1].Error == nil || captures[1].Error.Code != "stale" {
		t.Fatal("wrong state, sanitization or stale result")
	}
	if _, err := parseCaptures([]byte(strings.Replace(raw, "F\t1", "F\t0", 1)), refs); err == nil {
		t.Fatal("accepted duplicate result")
	}
	if _, err := parseCaptures([]byte("V\t1\nP\t"+b64("1 0 sh")+"\n"), refs); err == nil {
		t.Fatal("accepted missing results")
	}
}

func TestProcessArgumentRecordLimits(t *testing.T) {
	var snapshot strings.Builder
	records := [][]string{}
	for pid := 1; pid <= 129; pid++ {
		fmt.Fprintf(&snapshot, "%d 0 node\n", pid)
		records = append(records, []string{"A", fmt.Sprint(pid), b64("synthetic")})
	}
	records = append([][]string{{"P", b64(strings.TrimSpace(snapshot.String()))}}, records...)
	if _, err := parseProcesses(records); err == nil {
		t.Fatal("accepted more than 128 argument records")
	}
	records = records[:129]
	if _, err := parseProcesses(records); err != nil {
		t.Fatal("rejected the allowed argument record limit")
	}
	records = append(records, records[1])
	if _, err := parseProcesses(records); err == nil {
		t.Fatal("accepted duplicate argument records")
	}
}
