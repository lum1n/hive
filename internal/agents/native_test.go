package agents

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/execx"
	"github.com/lum1n/hive/internal/sshx"
	"github.com/lum1n/hive/internal/tmux"
)

func nativeFixture(t *testing.T) (config.Host, string) {
	t.Helper()
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is unavailable")
	}
	dir, err := os.MkdirTemp("", "ha-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "tmux.sock")
	t.Cleanup(func() {
		exec.Command(bin, "-S", socket, "kill-server").Run()
		if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
		if err := os.Remove(dir); err != nil {
			t.Error(err)
		}
	})
	cmd := exec.Command(bin, "-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", "fixture", "-x", "80", "-y", "24", "cat")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture start: %v %s", err, out)
	}
	socket, err = filepath.EvalSymlinks(socket)
	if err != nil {
		t.Fatal(err)
	}
	host := config.Host{ID: "local", Local: true, Tmux: bin, Socket: socket}
	pane := nativeRun(t, host, "display-message", "-p", "#{pane_id}")
	nativeRun(t, host, "set-option", "-p", "-t", pane, "@agent-overview-kind", "copilot")
	return host, pane
}

func nativeRun(t *testing.T, host config.Host, args ...string) string {
	t.Helper()
	command := exec.Command(host.Tmux, append([]string{"-S", host.Socket}, args...)...)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture command %s: %v", args[0], err)
	}
	return strings.TrimSpace(string(out))
}

func syntheticProcesses(ctx context.Context, name string, args ...string) execx.Result {
	args = append([]string(nil), args...)
	if name != "sh" || len(args) != 2 {
		return execx.Result{Err: fmt.Errorf("unexpected fixture invocation")}
	}
	args[1] = strings.ReplaceAll(args[1], processProbe, "\nprintf 'P\\t"+b64("1 0 sh")+"\\n'\n")
	return execx.Bounded(MaxInventoryBytes, 8192)(ctx, name, args...)
}

func TestNativeInventoryCaptureRenameAndGone(t *testing.T) {
	host, pane := nativeFixture(t)
	client := Client{Config: config.Config{Hosts: []config.Host{host}}, Runner: syntheticProcesses}
	nativeRun(t, host, "new-session", "-d", "-s", "linked", "cat")
	nativeRun(t, host, "link-window", "-s", "fixture:0", "-t", "linked:1")
	listing, err := client.List(context.Background(), "")
	if err != nil || listing.Hosts[0].Error != nil {
		t.Fatalf("listing failed: %v %+v", err, listing.Hosts[0].Error)
	}
	if len(listing.Hosts[0].Servers) != 1 || len(listing.Hosts[0].Servers[0].Agents) != 1 {
		t.Fatal("linked pane was not deduplicated")
	}
	agent := listing.Hosts[0].Servers[0].Agents[0]
	if len(agent.Members) != 2 {
		t.Fatal("missing linked memberships")
	}
	nativeRun(t, host, "send-keys", "-t", pane, "-l", "synthetic-agent-output")
	nativeRun(t, host, "send-keys", "-t", pane, "Enter")
	response, err := client.Capture(context.Background(), []string{agent.ID}, 5)
	if err != nil || response.Captures[0].Error != nil {
		t.Fatalf("capture failed: %v %+v", err, response.Captures[0].Error)
	}
	if !strings.Contains(response.Captures[0].Text, "synthetic-agent-output") ||
		len(strings.Split(response.Captures[0].Text, "\n")) > 5 {
		t.Fatal("capture text or row bound incorrect")
	}
	nativeRun(t, host, "rename-session", "-t", "fixture", "renamed space ; literal")
	nativeRun(t, host, "rename-window", "-t", pane, "renamed-window")
	if _, _, err := client.Resolve(context.Background(), agent.ID); err != nil {
		t.Fatal("rename invalidated stable identity")
	}
	identity := nativeRun(t, host, "display-message", "-p", "-t", pane, "#{pane_id} #{window_id} #{pane_width} #{pane_height}")
	if _, err := client.Capture(context.Background(), []string{agent.ID}, 200); err != nil {
		t.Fatal(err)
	}
	if nativeRun(t, host, "display-message", "-p", "-t", pane, "#{pane_id} #{window_id} #{pane_width} #{pane_height}") != identity {
		t.Fatal("capture mutated source")
	}
	nativeRun(t, host, "set-option", "-p", "-t", pane, "@agent-overview-kind", "off")
	response, err = client.Capture(context.Background(), []string{agent.ID}, 200)
	if err != nil || response.Captures[0].Error == nil || response.Captures[0].Error.Code != "gone" {
		t.Fatal("excluded pane was still captured")
	}
}

func TestNativeServerCollisionAndRestart(t *testing.T) {
	first, _ := nativeFixture(t)
	second, _ := nativeFixture(t)
	second.ID = "second"
	client := Client{Config: config.Config{Hosts: []config.Host{first, second}}, Runner: syntheticProcesses}
	listing, err := client.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range listing.Hosts {
		if host.Error != nil || len(host.Servers) != 1 || len(host.Servers[0].Agents) != 1 {
			t.Fatalf("fixture inventory failed: %+v", host.Error)
		}
	}
	a, b := listing.Hosts[0].Servers[0].Agents[0], listing.Hosts[1].Servers[0].Agents[0]
	if a.Pane != b.Pane || a.ID == b.ID {
		t.Fatal("server-qualified identity collision")
	}
	response, err := client.Capture(context.Background(), []string{a.ID, b.ID}, 200)
	if err != nil || response.Captures[0].Error != nil || response.Captures[1].Error != nil {
		t.Fatal("multi-host capture failed")
	}
	nativeRun(t, first, "kill-server")
	os.Remove(first.Socket)
	nativeRun(t, first, "-f", "/dev/null", "new-session", "-d", "-s", "replacement", "cat")
	response, err = client.Capture(context.Background(), []string{a.ID, b.ID}, 200)
	if err != nil || response.Captures[0].Error == nil || response.Captures[0].Error.Code != "stale" ||
		response.Captures[1].Error != nil {
		t.Fatal("restart did not invalidate only the stale target")
	}
}

func TestNativeProbeSyntax(t *testing.T) {
	host := config.Host{ID: "local", Local: true, Socket: "/fixture with spaces.sock"}
	ref := testReference(host.ID, host.Socket)
	for index, script := range []string{inventoryScript(host), captureScript(host, []Reference{ref}, 200), targetScript(host, ref)} {
		if out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Fatalf("invalid POSIX probe syntax (%d): %s", index, out)
		}

	}
}

func TestNativeMultipleServersOnOneHost(t *testing.T) {
	first, _ := nativeFixture(t)
	second, _ := nativeFixture(t)
	host := first
	host.Socket = ""
	calls := 0
	client := Client{Config: config.Config{Hosts: []config.Host{host}},
		Runner: func(ctx context.Context, name string, args ...string) execx.Result {
			calls++
			args = append([]string(nil), args...)
			prelude := "HIVE_TMUX_BIN=" + sshx.SingleQuote(host.Tmux) +
				"\nHIVE_TMUX_SOCK=\nHIVE_TMUX_L=\nHIVE_TMUX_SOCKS=" +
				sshx.SingleQuote(first.Socket+"\n"+second.Socket+"\n"+first.Socket) + "\n"
			args[1] = strings.ReplaceAll(args[1], tmux.Prelude(host.Tmux, ""), prelude)
			return syntheticProcesses(ctx, name, args...)
		}}
	listing, err := client.List(context.Background(), "")
	if err != nil || listing.Hosts[0].Error != nil || len(listing.Hosts[0].Servers) != 2 {
		t.Fatal("multi-server host discovery failed")
	}
	a, b := listing.Hosts[0].Servers[0].Agents[0], listing.Hosts[0].Servers[1].Agents[0]
	if a.Pane != b.Pane || a.ID == b.ID {
		t.Fatal("same-host pane identities collided")
	}
	calls = 0
	response, err := client.Capture(context.Background(), []string{a.ID, b.ID}, 200)
	if err != nil || calls != 1 || response.Captures[0].Error != nil || response.Captures[1].Error != nil {
		t.Fatal("same-host cross-server captures were not batched")
	}
}

func TestNativeWrapperFilteringAndLimits(t *testing.T) {
	snapshot := "10 1 sh\n11 10 node\n12 11 bun\n13 10 /fixture with spaces/node\n14 17 MainThread\n15 20 MainThread\n16 10 bwrap\n17 16 bwrap\n20 1 node\n30 31 node\n31 30 node"
	for _, roots := range []string{"", " 10"} {
		script := "_processes=" + sshx.SingleQuote(snapshot) + "\n_roots=" + sshx.SingleQuote(roots) +
			"\nhive_b64() { base64 | tr -d '\\r\\n'; }\nps() { printf 'synthetic-wrapper'; }\n" + wrapperProbe
		raw, err := exec.Command("sh", "-c", script).Output()
		if err != nil {
			t.Fatal("synthetic wrapper probe failed")
		}
		seen := map[string]bool{}
		for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
			if line == "" {
				continue
			}
			fields := strings.Split(line, "\t")
			if len(fields) != 3 || fields[0] != "A" || seen[fields[1]] {
				t.Fatal("invalid wrapper record")
			}
			seen[fields[1]] = true
		}
		if roots == "" && len(seen) != 0 || roots != "" && (len(seen) != 4 || !seen["11"] || !seen["12"] || !seen["13"] || !seen["14"]) {
			t.Fatal("wrapper arguments were not restricted to pane descendants")
		}
	}
	var many strings.Builder
	for pid := 100; pid < 230; pid++ {
		fmt.Fprintf(&many, "%d 10 node\n", pid)
	}
	for _, fixture := range []struct{ snapshot, args string }{
		{snapshot: many.String(), args: "synthetic"},
		{snapshot: "11 10 MainThread", args: strings.Repeat("x", 8193)},
	} {
		script := "_processes=" + sshx.SingleQuote(fixture.snapshot) +
			"\n_roots='10'\nhive_b64() { base64 | tr -d '\\r\\n'; }\nps() { printf %s " +
			sshx.SingleQuote(fixture.args) + "; }\n" + wrapperProbe
		if err := exec.Command("sh", "-c", script).Run(); execx.ExitCode(err) != 5 {
			t.Fatal("wrapper limit was not rejected explicitly")
		}
	}
}

func TestNativeCaptureByteAndDimensionLimits(t *testing.T) {
	host, pane := nativeFixture(t)
	client := Client{Config: config.Config{Hosts: []config.Host{host}}, Runner: syntheticProcesses}
	listing, err := client.List(context.Background(), "")
	if err != nil || listing.Hosts[0].Error != nil {
		t.Fatal("fixture discovery failed")
	}
	id := listing.Hosts[0].Servers[0].Agents[0].ID
	nativeRun(t, host, "resize-window", "-t", pane, "-x", "1024", "-y", "128")
	program := "BEGIN { for (i=0;i<24000;i++) printf \"%s\", \"\u754c\" }"
	nativeRun(t, host, "respawn-pane", "-k", "-t", pane,
		"awk "+sshx.SingleQuote(program)+"; exec cat")
	var capture Capture
	for range 50 {
		response, err := client.Capture(context.Background(), []string{id}, MaxLines)
		if err != nil || response.Captures[0].Error != nil {
			t.Fatal("large synthetic capture failed")
		}
		capture = response.Captures[0]
		if capture.Truncated {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !capture.Truncated || len(capture.Text) > MaxBytes || !utf8.ValidString(capture.Text) {
		t.Fatal("native capture did not enforce the UTF-8 byte bound")
	}
	for _, size := range [][2]string{{"4097", "24"}, {"80", "513"}} {
		nativeRun(t, host, "resize-window", "-t", pane, "-x", size[0], "-y", size[1])
		response, err := client.Capture(context.Background(), []string{id}, MaxLines)
		if err != nil || response.Captures[0].Error == nil || response.Captures[0].Error.Code != "output_limit" {
			t.Fatal("excessive source dimensions were not rejected")
		}
	}

}

func TestNativeLargeLinkedInventoryAndMetadata(t *testing.T) {
	host, pane := nativeFixture(t)
	for index := 1; index < 55; index++ {
		nativeRun(t, host, "new-session", "-d", "-t", "fixture", "-s", fmt.Sprintf("linked-%d", index))
	}
	owned := nativeRun(t, host, "split-window", "-d", "-I", "-P", "-F", "#{pane_id}")
	nativeRun(t, host, "set-option", "-p", "-t", owned, "@agent-overview-owned", "1")
	name := "fixture \u754c,\tnew\nline: $(not-a-command)"
	nativeRun(t, host, "rename-window", "-t", pane, name)
	name = nativeRun(t, host, "display-message", "-p", "-t", pane, "#{window_name}")
	client := Client{Config: config.Config{Hosts: []config.Host{host}},
		Runner: syntheticProcesses, Timeout: 3 * time.Second}
	started := time.Now()
	response, err := client.List(context.Background(), "")
	elapsed := time.Since(started)
	if err != nil || response.Hosts[0].Error != nil || len(response.Hosts[0].Servers) != 1 {
		t.Fatal("large linked inventory did not meet the three-second host deadline")
	}
	found := response.Hosts[0].Servers[0].Agents
	if len(found) != 1 || len(found[0].Members) != 55 {
		t.Fatal("large inventory dropped memberships or included empty owned panes")
	}
	for _, member := range found[0].Members {
		if member.WindowName != display(name) {
			t.Fatalf("synthetic window label: got %q, expected %q", member.WindowName, display(name))
		}
	}
	if strings.Count(inventoryScript(host), "list-panes") != 2 {
		t.Fatal("inventory no longer uses two batched pane reads per server")
	}
	t.Logf("110 pane memberships inventoried in %s", elapsed)
}

func TestNativeUnicodeProbeInCLocale(t *testing.T) {
	host, pane := nativeFixture(t)
	name := "fixture \u754c \U0001f916"
	text := "synthetic-\u754c-\U0001f916"
	nativeRun(t, host, "rename-session", "-t", "fixture", name)
	nativeRun(t, host, "rename-window", "-t", pane, name)
	nativeRun(t, host, "send-keys", "-t", pane, "-l", text)
	nativeRun(t, host, "send-keys", "-t", pane, "Enter")
	t.Setenv("LC_ALL", "C")
	t.Setenv("LC_CTYPE", "C")
	t.Setenv("LANG", "C")
	client := Client{Config: config.Config{Hosts: []config.Host{host}}, Runner: syntheticProcesses}
	listing, err := client.List(context.Background(), "")
	if err != nil || listing.Hosts[0].Error != nil || len(listing.Hosts[0].Servers) != 1 ||
		len(listing.Hosts[0].Servers[0].Agents) != 1 {
		t.Fatal("Unicode inventory failed in a non-UTF-8 locale")
	}
	agent := listing.Hosts[0].Servers[0].Agents[0]
	if len(agent.Members) != 1 || agent.Members[0].SessionName != name || agent.Members[0].WindowName != name {
		t.Fatal("Unicode metadata was replaced or incorrectly framed")
	}
	if _, _, err := client.Resolve(context.Background(), agent.ID); err != nil {
		t.Fatal("Unicode target resolution failed in a non-UTF-8 locale")
	}
	for range 50 {
		response, err := client.Capture(context.Background(), []string{agent.ID}, 20)
		if err != nil || response.Captures[0].Error != nil {
			t.Fatal("Unicode capture failed in a non-UTF-8 locale")
		}
		if strings.Contains(response.Captures[0].Text, text) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Unicode captured output was replaced in a non-UTF-8 locale")
}
