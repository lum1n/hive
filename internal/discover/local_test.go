package discover

import (
	"context"
	"os/exec"
	"testing"

	"github.com/lum1n/hive/internal/cache"
	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/execx"
	"github.com/lum1n/hive/internal/sshx"
)

func TestRefreshLocalTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := "hive-test-" + t.Name()
	cmd := exec.Command("tmux", "-L", socket, "-f", "/dev/null", "new-session", "-d", "-s", "probe")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	})

	host := config.Host{ID: "local", Local: true, Tmux: "tmux", Socket: socket}
	snap := Refresh(context.Background(), execx.Default, sshx.Options{}, host)
	if snap.Status != cache.StatusOnline {
		t.Fatalf("status=%s err=%s", snap.Status, snap.Error)
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].Name != "probe" {
		t.Fatalf("sessions=%+v", snap.Sessions)
	}
}
