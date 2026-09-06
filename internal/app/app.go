package app

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/lum1n/hive/internal/attach"
	"github.com/lum1n/hive/internal/cache"
	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/picker"
	"github.com/lum1n/hive/internal/prefix"
	"github.com/lum1n/hive/internal/sshx"
	"github.com/lum1n/hive/internal/workspace"
	"golang.org/x/term"
)

type Options struct {
	Config config.Config
	Store  *cache.Store
	SSH    sshx.Options
}

func Run(ctx context.Context, opt Options) error {
	pb, err := opt.Config.PrefixByte()
	if err != nil {
		return err
	}
	att := attach.Options{SSH: opt.SSH, Prefix: pb}
	last, _ := opt.Store.LastWorkspace()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		choice, err := picker.Run(ctx, picker.Options{
			Config: opt.Config,
			Store:  opt.Store,
			SSH:    opt.SSH,
			Last:   last,
		})
		if err != nil {
			return err
		}
		switch choice.Action {
		case picker.ActionQuit:
			return nil
		case picker.ActionKill:
			if err := attach.Kill(ctx, att, choice.Host, choice.Workspace.Session); err != nil {
				fmt.Fprintf(os.Stderr, "hive: %v\n", err)
			}
			continue
		case picker.ActionRename:
			if err := attach.Rename(ctx, att, choice.Host, choice.Workspace.Session, choice.NewName); err != nil {
				fmt.Fprintf(os.Stderr, "hive: %v\n", err)
				continue
			}
			last = workspace.ID{Host: choice.Host.ID, Session: choice.NewName}
			_ = opt.Store.SetLastWorkspace(last)
			continue
		case picker.ActionCreate:
			if err := attach.Create(ctx, att, choice.Host, choice.NewName); err != nil {
				fmt.Fprintf(os.Stderr, "hive: %v\n", err)
				continue
			}
			choice.Workspace = workspace.ID{Host: choice.Host.ID, Session: choice.NewName}
			fallthrough
		case picker.ActionAttach:
			last = choice.Workspace
			_ = opt.Store.SetLastWorkspace(last)
			if err := attachLoop(ctx, att, opt.Config, choice.Host, last); err != nil {
				fmt.Fprintf(os.Stderr, "hive: %v\n", err)
			}
		}
	}
}

func attachLoop(ctx context.Context, att attach.Options, cfg config.Config, host config.Host, id workspace.ID) error {
	attempt := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		out, err := attach.Session(ctx, att, host, id)
		switch out {
		case attach.OutcomeDetached, attach.OutcomeExited:
			return nil
		case attach.OutcomeGone:
			fmt.Fprintf(os.Stderr, "hive: %s is gone\n", id.Display())
			return nil
		case attach.OutcomeDisconnected:
			wait := attach.Backoff(attempt)
			attempt++
			if err != nil {
				fmt.Fprintf(os.Stderr, "hive: %s · %v\n", id.Display(), err)
			}
			if abort := waitReconnect(ctx, cfg, id, wait); abort {
				return nil
			}
		}
	}
}

func waitReconnect(ctx context.Context, cfg config.Config, id workspace.ID, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	fmt.Fprintf(os.Stderr, "hive: %s · reconnecting in %s  (q picker)\n", id.Display(), wait.Round(time.Second))
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return true
		case <-t.C:
			return false
		}
	}
	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		time.Sleep(wait)
		return false
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), old) }()

	buf := make([]byte, 1)
	for time.Now().Before(deadline) {
		remain := time.Until(deadline)
		_ = os.Stdin.SetReadDeadline(time.Now().Add(min(remain, 200*time.Millisecond)))
		n, _ := os.Stdin.Read(buf)
		if n == 0 {
			select {
			case <-ctx.Done():
				return true
			default:
				continue
			}
		}
		b := buf[0]
		pb, _ := prefix.Parse(cfg.Prefix)
		if b == 'q' || b == 'Q' || b == 3 || b == pb {
			return true
		}
	}
	return false
}
