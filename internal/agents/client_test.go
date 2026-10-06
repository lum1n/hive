package agents

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/execx"
)

func TestHostResultsAndConcurrency(t *testing.T) {
	cfg := config.Config{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		cfg.Hosts = append(cfg.Hosts, config.Host{ID: id, SSH: id})
	}
	var active, peak atomic.Int32
	client := Client{Config: cfg, Runner: func(ctx context.Context, name string, args ...string) execx.Result {
		n := active.Add(1)
		defer active.Add(-1)
		for p := peak.Load(); n > p; p = peak.Load() {
			if peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		if strings.Contains(strings.Join(args, " "), " f -- ") {
			return execx.Result{Err: errors.New("failed"), Stderr: []byte("permission denied: synthetic")}
		}
		return execx.Result{Stdout: []byte(inventoryFixture())}
	}}
	response, err := client.List(context.Background(), "")
	if err != nil || len(response.Hosts) != 6 || peak.Load() > 4 {
		t.Fatal("wrong concurrency or results")
	}
	if response.Hosts[5].Status != "auth" || response.Hosts[0].Status != "online" {
		t.Fatal("partial failure lost healthy hosts")
	}
	if _, err := client.List(context.Background(), "unknown"); err == nil {
		t.Fatal("accepted unconfigured host")
	}
}

func TestCaptureBatchesAndValidation(t *testing.T) {
	var calls atomic.Int32
	client := Client{Config: config.Config{Hosts: []config.Host{{ID: "devbox", SSH: "devbox"}}},
		Runner: func(ctx context.Context, name string, args ...string) execx.Result {
			calls.Add(1)
			return execx.Result{Stdout: []byte("V\t1\nP\t" + b64("1 0 copilot") +
				"\nC\t0\t0\t1\t\t" + b64("copilot") + "\t" + b64("first") +
				"\nC\t1\t0\t1\t\t" + b64("copilot") + "\t" + b64("second") + "\n")}
		}}
	first, second := testReference("devbox", "/one.sock").ID(), testReference("devbox", "/two.sock").ID()
	response, err := client.Capture(context.Background(), []string{first, second}, 200)
	if err != nil || calls.Load() != 1 || response.Captures[0].Text != "first" || response.Captures[1].Text != "second" {
		t.Fatal("capture did not batch per host")
	}
	for _, ids := range [][]string{{}, {"invalid"}, {first, first}, {testReference("unknown", "/one.sock").ID()}} {
		if _, err := client.Capture(context.Background(), ids, 200); err == nil {
			t.Fatal("accepted invalid target batch")
		}
	}
	for _, lines := range []int{0, -1, MaxLines + 1} {
		if _, err := client.Capture(context.Background(), []string{first}, lines); err == nil {
			t.Fatal("accepted invalid line limit")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("invalid input contacted hosts")
	}
}

func TestCancelledAndOversizedDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := Client{Config: config.Config{Hosts: []config.Host{{ID: "local", Local: true}}},
		Runner: func(ctx context.Context, name string, args ...string) execx.Result {
			return execx.Result{Err: ctx.Err()}
		}}
	response, err := client.List(ctx, "")
	if err != nil || response.Hosts[0].Error.Code != "cancelled" {
		t.Fatal("cancellation was not explicit")
	}
	client.Runner = func(ctx context.Context, name string, args ...string) execx.Result {
		return execx.Result{Stdout: make([]byte, MaxInventoryBytes+1)}
	}
	response, err = client.List(context.Background(), "")
	if err != nil || response.Hosts[0].Error.Code != "output_limit" {
		t.Fatal("oversized output was not rejected")
	}
}

func TestEquivalentReferenceIdentity(t *testing.T) {
	ref := testReference("devbox", "/fixture.sock")
	alternate := "hive-agent-v1." + base64.RawURLEncoding.EncodeToString([]byte(
		`{"pane":"%1","generation":"123:1700000000","socket":"/fixture.sock","host":"devbox","version":1}`))
	client := Client{Config: config.Config{Hosts: []config.Host{{ID: ref.Host, Local: true}}},
		Runner: func(ctx context.Context, name string, args ...string) execx.Result {
			return execx.Result{Stdout: []byte("V\t1\nP\t" + b64("1 0 copilot") +
				"\nC\t0\t0\t1\t\t" + b64("copilot") + "\t" + b64("synthetic") + "\n")}
		}}
	response, err := client.Capture(context.Background(), []string{alternate}, 200)
	if err != nil || response.Captures[0].Error != nil || response.Captures[0].ID != alternate {
		t.Fatal("capture did not preserve the requested opaque reference")
	}
	if _, err := client.Capture(context.Background(), []string{ref.ID(), alternate}, 200); err == nil {
		t.Fatal("equivalent references bypassed duplicate validation")
	}
	client.Runner = func(ctx context.Context, name string, args ...string) execx.Result {
		return execx.Result{Stdout: []byte(inventoryFixture())}
	}
	if _, resolved, err := client.Resolve(context.Background(), alternate); err != nil || resolved != ref {
		t.Fatal("equivalent reference failed fresh resolution")
	}
}
