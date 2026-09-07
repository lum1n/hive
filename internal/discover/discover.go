package discover

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/lum1n/hive/internal/cache"
	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/execx"
	"github.com/lum1n/hive/internal/sshx"
	"github.com/lum1n/hive/internal/tmux"
)

const HostTimeout = 12 * time.Second

type Result struct {
	Host     string
	Snapshot cache.HostSnapshot
}

func Refresh(ctx context.Context, run execx.Runner, opt sshx.Options, host config.Host) cache.HostSnapshot {
	if run == nil {
		run = execx.Default
	}
	name, args := listInvocation(opt, host)
	res := run(ctx, name, args...)
	now := time.Now()
	if res.Err != nil {
		msg := strings.TrimSpace(res.ErrText())
		if tmux.MissingServer(msg) {
			return cache.HostSnapshot{Time: now, Status: cache.StatusOnline, Error: compact(msg), Sessions: []cache.Session{}}
		}
		return cache.HostSnapshot{
			Time:   now,
			Status: classify(msg),
			Error:  compact(msg),
		}
	}
	sessions := tmux.ParseList(string(res.Stdout))
	err := ""
	if len(sessions) == 0 {
		err = compact(string(res.Stderr))
		if err == "" {
			err = "no sessions"
		}
	}
	return cache.HostSnapshot{
		Time:     now,
		Status:   cache.StatusOnline,
		Error:    err,
		Sessions: sessions,
	}
}

func RefreshAll(ctx context.Context, run execx.Runner, opt sshx.Options, hosts []config.Host, concurrency int, out chan<- Result) {
	if concurrency < 1 {
		concurrency = 8
	}
	if concurrency > len(hosts) && len(hosts) > 0 {
		concurrency = len(hosts)
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, host := range hosts {
		wg.Add(1)
		sem <- struct{}{}
		go func(h config.Host) {
			defer wg.Done()
			defer func() { <-sem }()
			hctx, cancel := context.WithTimeout(ctx, HostTimeout)
			defer cancel()
			snap := Refresh(hctx, run, opt, h)
			select {
			case out <- Result{Host: h.ID, Snapshot: snap}:
			case <-ctx.Done():
			}
		}(host)
	}
	wg.Wait()
}

func listInvocation(opt sshx.Options, host config.Host) (string, []string) {
	local := tmux.Cmd(host.Local, host.TmuxBin(), host.Socket, "list-sessions", "-F", tmux.ListFormat)
	if host.Local {
		return local[0], local[1:]
	}
	return "ssh", sshx.ExecArgs(opt, host.ID, host.Destination(), host.ControlPath, local)
}

func classify(message string) cache.Status {
	low := strings.ToLower(message)
	for _, s := range []string{
		"permission denied",
		"host key verification",
		"too many authentication",
		"publickey",
		"authentication failed",
	} {
		if strings.Contains(low, s) {
			return cache.StatusAuth
		}
	}
	return cache.StatusOffline
}

func compact(message string) string {
	message = strings.TrimSpace(message)
	message = strings.ReplaceAll(message, "\n", " ")
	if len(message) > 160 {
		return message[:157] + "..."
	}
	return message
}
