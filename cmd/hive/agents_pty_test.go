package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestAgentPickerPTYHelper(t *testing.T) {
	path := os.Getenv("HIVE_PICKER_TEST_CONFIG")
	if path == "" {
		t.Skip("PTY subprocess only")
	}
	if err := runArgs([]string{"agents", "--config", path}, os.Stdout, os.Stderr); err != nil {
		t.Fatal("interactive agent command failed")
	}
}

func TestAgentPickerPTYAttachAndReturn(t *testing.T) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("", "hap-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "tmux.sock")
	path := filepath.Join(dir, "hive.toml")
	t.Cleanup(func() {
		exec.Command(bin, "-S", socket, "kill-server").Run()
		for _, file := range []string{path, socket} {
			if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
				t.Error(err)
			}
		}
		if err := os.Remove(dir); err != nil {
			t.Error(err)
		}
	})
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(bin, append([]string{"-S", socket}, args...)...).Output()
		if err != nil {
			t.Fatalf("synthetic tmux command failed: %s", args[0])
		}
		return strings.TrimSpace(string(out))
	}
	run("-f", "/dev/null", "new-session", "-d", "-s", "picker-fixture", "-c", dir, "cat")
	run("set-option", "-p", "@agent-overview-kind", "copilot")
	target := run("split-window", "-d", "-h", "-c", dir, "-P", "-F", "#{pane_id}", "cat")
	run("set-option", "-p", "-t", target, "@agent-overview-kind", "cursor")
	raw := fmt.Sprintf("prefix = \"ctrl-space\"\n[[hosts]]\nid = \"fixture\"\nlocal = true\ntmux = %q\nsocket = %q\n", bin, socket)
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAgentPickerPTYHelper$")
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "TMUX=") && !strings.HasPrefix(env, "TMUX_PANE=") &&
			!strings.HasPrefix(env, "TERM=") && !strings.HasPrefix(env, "HIVE_PICKER_TEST_CONFIG=") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "TERM=xterm-256color", "HIVE_PICKER_TEST_CONFIG="+path)
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 100})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var output bytes.Buffer
	var mu sync.Mutex
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 4096)
		for {
			n, err := terminal.Read(buf)
			mu.Lock()
			output.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		terminal.Close()
		<-readerDone
	})
	wait := func(check func() bool, label string) {
		t.Helper()
		for range 500 {
			if check() {
				return
			}
			if ctx.Err() != nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("PTY agent picker did not %s", label)
	}
	hasText := func(text string) bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(output.String(), text)
	}
	write := func(text string) {
		t.Helper()
		if _, err := io.WriteString(terminal, text); err != nil {
			t.Fatal(err)
		}
	}
	wait(func() bool { return hasText("2 agents") && hasText("cursor") }, "show both synthetic agents")
	write("cursor")
	wait(func() bool { return hasText("1 agents") }, "filter by agent kind")
	write("\r")
	wait(func() bool { return run("list-clients", "-F", "#{pane_id}") == target }, "attach to the exact selected pane")
	write("picker-input-marker\r")
	wait(func() bool { return strings.Contains(run("capture-pane", "-p", "-t", target), "picker-input-marker") }, "deliver input to the selected synthetic agent")
	mu.Lock()
	output.Reset()
	mu.Unlock()
	write("\x00")
	wait(func() bool { return hasText("hive agents") && hasText("2 agents") }, "return to the picker after detach")
	newPane := run("split-window", "-d", "-v", "-t", target, "-c", dir, "-P", "-F", "#{pane_id}", "cat")
	run("set-option", "-p", "-t", newPane, "@agent-overview-kind", "claude")
	mu.Lock()
	output.Reset()
	mu.Unlock()
	write("\x12")
	wait(func() bool { return hasText("3 agents") }, "discover a new agent on interactive refresh")
	write("\x03")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("agent picker did not quit cleanly")
		}
	case <-ctx.Done():
		t.Fatal("agent picker did not quit")
	}
}
