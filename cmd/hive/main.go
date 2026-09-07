package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/lum1n/hive/internal/app"
	"github.com/lum1n/hive/internal/cache"
	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/discover"
	"github.com/lum1n/hive/internal/sshx"
	"golang.org/x/term"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "hive: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", config.DefaultPath(), "config file")
	initCfg := flag.Bool("init", false, "write an example config and exit")
	dump := flag.Bool("dump", false, "print host snapshots and exit")
	showVer := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVer {
		fmt.Println(version())
		return nil
	}

	if *initCfg {
		if err := config.WriteExample(*configPath); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", *configPath)
		return nil
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

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		v := bi.Main.Version
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				v += " " + s.Value[:7]
			}
		}
		if v != "" && v != "(devel)" {
			return v
		}
		if v == "(devel)" {
			for _, s := range bi.Settings {
				if s.Key == "vcs.revision" && len(s.Value) >= 7 {
					return "hive " + s.Value[:7]
				}
			}
		}
	}
	return "hive"
}
