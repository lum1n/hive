package attach

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/lum1n/hive/internal/agents"
	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/execx"
)

func TestAgentClientRequiresUnambiguousOrigin(t *testing.T) {
	t.Setenv("TMUX_PANE", "%1")
	opt := Options{Runner: func(ctx context.Context, name string, args ...string) execx.Result {
		return execx.Result{Stdout: []byte("first\t%1\nsecond\t%1\n")}
	}}
	if _, err := agentClient(context.Background(), opt, "/fixture.sock", ""); err == nil {
		t.Fatal("ambiguous origin was accepted")
	}
	if name, err := agentClient(context.Background(), opt, "/fixture.sock", "second"); err != nil || name != "second" {
		t.Fatal("explicit origin did not resolve")
	}
	if _, err := agentClient(context.Background(), opt, "/fixture.sock", "unknown"); err == nil {
		t.Fatal("unavailable client was accepted")
	}
}

func TestNativeAgentFocusAndAtomicGenerationGuard(t *testing.T) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is unavailable")
	}
	dir, err := os.MkdirTemp("", "haf-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "tmux.sock")
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(bin, append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("fixture command %s: %v", args[0], err)
		}
		return strings.TrimSpace(string(out))
	}
	run("-f", "/dev/null", "new-session", "-d", "-s", "fixture", "cat")
	socket, err = filepath.EvalSymlinks(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		exec.Command(bin, "-S", socket, "kill-server").Run()
		if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
		if err := os.Remove(dir); err != nil {
			t.Error(err)
		}
	})
	source := run("display-message", "-p", "#{pane_id}")
	target := run("split-window", "-d", "-h", "-P", "-F", "#{pane_id}", "cat")
	generation := run("display-message", "-p", "#{pid}:#{start_time}")
	run("set-option", "-p", "-t", target, "@agent-overview-kind", "copilot")
	control := exec.Command(bin, "-S", socket, "-C", "attach-session", "-t", "fixture")
	input, err := control.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		input.Close()
		control.Process.Kill()
		control.Wait()
	})
	var name string
	for range 100 {
		name = run("list-clients", "-F", "#{client_name}")
		if name != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if name == "" {
		t.Fatal("control client did not attach")
	}
	t.Setenv("TMUX", socket+",0,0")
	t.Setenv("TMUX_PANE", source)
	host := config.Host{ID: "local", Local: true, Tmux: bin, Socket: socket}
	ref := agents.Reference{Version: 1, Host: host.ID, Socket: socket, Generation: generation, Pane: target}
	encode := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	window, session := run("display-message", "-p", "-t", target, "#{window_id}"),
		run("display-message", "-p", "-t", target, "#{session_id}")
	raw := "V\t1\nP\t" + encode("1 0 sh") + "\nS\t" + encode(socket) + "\t" + generation + "\n" +
		strings.Join([]string{"N", target, window, session, "1", "0", "0", "0", encode("copilot"),
			encode("cat"), encode("/project"), encode("fixture"), encode("agents")}, "\t") + "\n"
	client := agents.Client{Config: config.Config{Hosts: []config.Host{host}},
		Runner: func(ctx context.Context, command string, args ...string) execx.Result {
			return execx.Result{Stdout: []byte(raw)}
		}}
	prelude, err := client.TargetScript(host, ref)
	if err != nil {
		t.Fatal(err)
	}
	stale := ref
	stale.Generation = "1:1"
	staleCmd := exec.Command("sh", "-c", prelude+focusScript(stale, name))
	if err := staleCmd.Run(); err == nil {
		t.Fatal("native stale-generation guard did not fail")
	}
	if run("display-message", "-p", "#{pane_id}") != source {
		t.Fatal("stale guard changed selection")
	}
	out, err := Agent(context.Background(), Options{}, client, ref.ID(), name)
	if err != nil || out != OutcomeSwitched {
		t.Fatalf("exact focus failed: %v (%s)", err, out)
	}
	if run("list-clients", "-F", "#{pane_id}") != target {
		t.Fatal("focus selected the wrong pane")
	}
	if _, err := exec.Command("sh", "-n", "-c", prelude+agentAttachScript(ref)).CombinedOutput(); err != nil {
		t.Fatal("agent attach shell syntax invalid")
	}
	terminalCmd := agentAttachCommand(Options{}.SSH, host, prelude+agentAttachScript(ref))
	terminalCmd.Env = append(dropEnv(terminalCmd.Env, "TERM"), "TERM=xterm-256color")
	terminal, err := pty.Start(terminalCmd)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		io.Copy(io.Discard, terminal)
		close(done)
	}()
	t.Cleanup(func() {
		terminalCmd.Process.Kill()
		terminalCmd.Wait()
		terminal.Close()
		<-done
	})
	attached := false
	for range 100 {
		if len(strings.Split(run("list-clients", "-F", "#{pane_id}"), "\n")) == 2 {
			attached = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !attached {
		t.Fatal("guarded attach did not establish a real PTY client")
	}
	if _, err := terminal.Write([]byte("native-input-marker\n")); err != nil {
		t.Fatal(err)
	}
	received := false
	for range 100 {
		if strings.Contains(run("capture-pane", "-p", "-t", target), "native-input-marker") {
			received = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !received {
		t.Fatal("guarded attach did not preserve terminal input")
	}
}

func TestAgentAttachExitClassification(t *testing.T) {
	for code, expected := range map[int]Outcome{8: OutcomeGone, 255: OutcomeDisconnected} {
		err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
		out, gotErr := agentAttachOutcome(OutcomeDisconnected, err)
		if out != expected || gotErr == nil {
			t.Fatal("attach exit classification lost stale/connection errors")
		}
	}
}
