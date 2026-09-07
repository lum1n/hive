package discover

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lum1n/hive/internal/cache"
	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/execx"
	"github.com/lum1n/hive/internal/sshx"
)

func TestRefreshOnline(t *testing.T) {
	t.Parallel()
	run := func(ctx context.Context, name string, args ...string) execx.Result {
		return execx.Result{Stdout: []byte("backend\t2\t0\n")}
	}
	host := config.Host{ID: "devbox", SSH: "devbox"}
	snap := Refresh(context.Background(), run, sshx.Options{RuntimeDir: t.TempDir()}, host)
	if snap.Status != cache.StatusOnline || len(snap.Sessions) != 1 || snap.Sessions[0].Name != "backend" {
		t.Fatalf("%+v", snap)
	}
}

func TestRefreshEmptyServer(t *testing.T) {
	t.Parallel()
	run := func(ctx context.Context, name string, args ...string) execx.Result {
		return execx.Result{Stderr: []byte("no server running on /tmp/tmux"), Err: errors.New("exit 1")}
	}
	host := config.Host{ID: "local", Local: true}
	snap := Refresh(context.Background(), run, sshx.Options{}, host)
	if snap.Status != cache.StatusOnline || len(snap.Sessions) != 0 || !strings.Contains(snap.Error, "no server running") {
		t.Fatalf("%+v", snap)
	}
}

func TestRefreshAuth(t *testing.T) {
	t.Parallel()
	run := func(ctx context.Context, name string, args ...string) execx.Result {
		return execx.Result{Stderr: []byte("Permission denied (publickey)."), Err: errors.New("exit 255")}
	}
	host := config.Host{ID: "prod", SSH: "prod"}
	snap := Refresh(context.Background(), run, sshx.Options{RuntimeDir: t.TempDir()}, host)
	if snap.Status != cache.StatusAuth {
		t.Fatalf("status=%s", snap.Status)
	}
}

func TestRefreshAll(t *testing.T) {
	t.Parallel()
	run := func(ctx context.Context, name string, args ...string) execx.Result {
		return execx.Result{Stdout: []byte("s\t1\t0\n")}
	}
	hosts := []config.Host{
		{ID: "a", Local: true},
		{ID: "b", Local: true},
	}
	ch := make(chan Result, 2)
	RefreshAll(context.Background(), run, sshx.Options{}, hosts, 2, ch)
	close(ch)
	n := 0
	for range ch {
		n++
	}
	if n != 2 {
		t.Fatalf("got %d", n)
	}
}

func TestRefreshUnparsedStdout(t *testing.T) {
	t.Parallel()
	run := func(ctx context.Context, name string, args ...string) execx.Result {
		return execx.Result{Stdout: []byte("not a session line\n")}
	}
	host := config.Host{ID: "box", SSH: "box"}
	snap := Refresh(context.Background(), run, sshx.Options{RuntimeDir: t.TempDir()}, host)
	if snap.Status != cache.StatusOnline || !strings.Contains(snap.Error, "unparsed:") {
		t.Fatalf("%+v", snap)
	}
}

func TestRefreshEmptyStdout(t *testing.T) {
	t.Parallel()
	run := func(ctx context.Context, name string, args ...string) execx.Result {
		return execx.Result{Stdout: []byte("")}
	}
	host := config.Host{ID: "box", SSH: "box"}
	snap := Refresh(context.Background(), run, sshx.Options{RuntimeDir: t.TempDir()}, host)
	if snap.Status != cache.StatusOnline || snap.Error != "no sessions" {
		t.Fatalf("%+v", snap)
	}
}

func TestClassifyOffline(t *testing.T) {
	t.Parallel()
	if classify("Connection timed out") != cache.StatusOffline {
		t.Fatal("timeout")
	}
}

func TestListInvocationRemoteWrapsDespiteLocalTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	name, args := listInvocation(sshx.Options{RuntimeDir: t.TempDir()}, config.Host{ID: "mac", SSH: "mac"})
	if name != "ssh" {
		t.Fatalf("name=%s", name)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "base64") || !strings.Contains(joined, "printf") {
		t.Fatalf("%s", joined)
	}
}
