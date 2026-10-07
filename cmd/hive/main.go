package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lum1n/hive/internal/agentcli"
	"github.com/lum1n/hive/internal/app"
	"github.com/lum1n/hive/internal/cache"
	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/discover"
	"github.com/lum1n/hive/internal/sshx"
	"github.com/lum1n/hive/internal/version"
	"golang.org/x/term"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "hive: %v\n", err)
		var exit *agentcli.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.Code)
		}
		os.Exit(1)
	}
}

func run() error {
	return runArgs(os.Args[1:], os.Stdout, os.Stderr)
}

func runArgs(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("hive", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", config.DefaultPath(), "config file")
	initCfg := flags.Bool("init", false, "write an example config and exit")
	dump := flags.Bool("dump", false, "print host snapshots and exit")
	showVer := flags.Bool("version", false, "print version and exit")
	var helpErr error
	flags.Usage = func() {
		helpErr = agentcli.WriteHelp(stderr, flags, `Usage: hive [flags]
       hive [--config PATH] agents [flags]
       hive [--config PATH] agents <command> [flags]

Run hive without arguments to open the interactive tmux session picker.
Run hive agents to open the interactive agent picker.
The agents commands also provide headless capabilities, list, capture, and attach.
Use hive agents --help or hive agents <command> --help for details.
Help works without configuration, a terminal, or host connections.
`, `
Examples:
  hive
  hive -init
  hive -dump
  hive agents
  hive agents list --json
  hive agents --help
`)
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return helpErr
		}
		return err
	}

	if *showVer {
		_, err := fmt.Fprintln(stdout, version.Full())
		return err
	}

	if *initCfg {
		if err := config.WriteExample(*configPath); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "wrote %s\n", *configPath)
		return err
	}

	if args := flags.Args(); len(args) > 0 {
		if args[0] == "version" {
			_, err := fmt.Fprintln(stdout, version.Full())
			return err
		}
		if args[0] != "agents" {
			return fmt.Errorf("unknown command; use hive agents or run hive without arguments")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return agentcli.Run(ctx, args[1:], *configPath, stdout, stderr)
	}

	if _, err := os.Stat(*configPath); err != nil {
		return fmt.Errorf("no config at %s\ncopy hive.toml.example or run: hive -init", *configPath)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	store, err := cache.New(cache.DefaultDir())
	if err != nil {
		return err
	}
	runtimeDir, err := sshx.RuntimeDir()
	if err != nil {
		return err
	}
	sshOpt := sshx.Options{
		RuntimeDir:     runtimeDir,
		ControlPersist: time.Duration(cfg.ControlPersist) * time.Second,
	}

	if *dump {
		return dumpHosts(cfg, sshOpt)
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("need a terminal")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return app.Run(ctx, app.Options{
		Config: cfg,
		Store:  store,
		SSH:    sshOpt,
	})
}

func dumpHosts(cfg config.Config, sshOpt sshx.Options) error {
	ctx, cancel := context.WithTimeout(context.Background(), discover.HostTimeout+2*time.Second)
	defer cancel()
	ch := make(chan discover.Result, len(cfg.Hosts))
	discover.RefreshAll(ctx, nil, sshOpt, cfg.Hosts, 8, ch)
	close(ch)
	byID := make(map[string]cache.HostSnapshot, len(cfg.Hosts))
	for r := range ch {
		byID[r.Host] = r.Snapshot
	}
	for _, h := range cfg.Hosts {
		snap := byID[h.ID]
		fmt.Printf("%s  %s", h.Display(), snap.Status)
		if snap.Error != "" {
			fmt.Printf("  %s", snap.Error)
		}
		fmt.Println()
		if len(snap.Sessions) == 0 {
			fmt.Println("  (no sessions)")
			continue
		}
		for _, s := range snap.Sessions {
			fmt.Printf("  %s  %d windows", s.Name, s.Windows)
			if s.Attached {
				fmt.Print("  attached")
			}
			fmt.Println()
		}
	}
	return nil
}
