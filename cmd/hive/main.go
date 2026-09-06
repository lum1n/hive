package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lum1n/hive/internal/app"
	"github.com/lum1n/hive/internal/cache"
	"github.com/lum1n/hive/internal/config"
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
	flag.Parse()

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
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("need a terminal")
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return app.Run(ctx, app.Options{
		Config: cfg,
		Store:  store,
		SSH: sshx.Options{
			RuntimeDir:     runtimeDir,
			ControlPersist: time.Duration(cfg.ControlPersist) * time.Second,
		},
	})
}
