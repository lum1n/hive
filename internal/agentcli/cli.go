package agentcli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/lum1n/hive/internal/agents"
	"github.com/lum1n/hive/internal/app"
	"github.com/lum1n/hive/internal/attach"
	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/sshx"
	"golang.org/x/term"
)

type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

type references []string

func (r *references) String() string { return "" }
func (r *references) Set(value string) error {
	if len(*r) >= agents.MaxTargets {
		return fmt.Errorf("at most %d targets are allowed", agents.MaxTargets)
	}
	*r = append(*r, value)
	return nil
}

func Run(ctx context.Context, args []string, configPath string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hive agents capabilities|list|capture|attach")
	}
	command := args[0]
	if command != "capabilities" && command != "list" && command != "capture" && command != "attach" {
		return fmt.Errorf("unknown agents command")
	}
	flags := flag.NewFlagSet("hive agents "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "print protocol-versioned JSON")
	path := flags.String("config", configPath, "Hive config file")
	timeout := flags.Duration("timeout", 12*time.Second, "per-host operation deadline (maximum 60s)")
	var host, initiatingClient *string
	var ids references
	lines := 200
	switch command {
	case "list":
		host = flags.String("host", "", "only this configured host")
	case "capture":
		flags.Var(&ids, "id", "agent reference; repeat for a batch")
		flags.IntVar(&lines, "lines", 200, "maximum captured rows")
	case "attach":
		flags.Var(&ids, "id", "agent reference")
		initiatingClient = flags.String("client", "", "initiating local tmux client")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if len(flags.Args()) != 0 || *timeout <= 0 || *timeout > 60*time.Second {
		return fmt.Errorf("unexpected arguments or invalid timeout")
	}
	if command == "attach" && (*jsonOutput || len(ids) != 1) {
		return fmt.Errorf("attach needs exactly one --id and does not support --json")
	}
	if command != "attach" && !*jsonOutput {
		return fmt.Errorf("headless agent commands require --json")
	}
	if command == "capabilities" {
		return json.NewEncoder(stdout).Encode(struct {
			Version    int      `json:"version"`
			Commands   []string `json:"commands"`
			Kinds      []string `json:"kinds"`
			MaxTargets int      `json:"max_targets"`
			MaxLines   int      `json:"max_lines"`
			MaxBytes   int      `json:"max_bytes"`
		}{agents.Version, []string{"list", "capture", "attach"}, agents.Kinds,
			agents.MaxTargets, agents.MaxLines, agents.MaxBytes})
	}
	if command == "capture" && (len(ids) == 0 || lines < 1 || lines > agents.MaxLines) {
		return fmt.Errorf("capture needs --id and 1-%d lines", agents.MaxLines)
	}
	for _, id := range ids {
		if _, err := agents.ParseReference(id); err != nil {
			return err
		}
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return fmt.Errorf("cannot load Hive configuration; check the path and TOML settings")
	}
	if command == "attach" && (!term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd()))) {
		return fmt.Errorf("agent attach needs a terminal")
	}
	if command == "list" && *host != "" {
		if _, ok := cfg.Host(*host); !ok {
			return fmt.Errorf("host is not configured")
		}
	}
	remote := false
	for _, h := range cfg.Hosts {
		selected := command == "list" && (*host == "" || *host == h.ID)
		for _, id := range ids {
			ref, _ := agents.ParseReference(id)
			selected = selected || ref.Host == h.ID
		}
		remote = remote || selected && !h.Local
	}
	opt := sshx.Options{ControlPersist: time.Duration(cfg.ControlPersist) * time.Second}
	if remote {
		opt.RuntimeDir, err = sshx.RuntimeDir()
		if err != nil {
			return fmt.Errorf("cannot prepare private SSH runtime")
		}
	}
	client := agents.Client{Config: cfg, SSH: opt, Timeout: *timeout}
	switch command {
	case "list":
		response, err := client.List(ctx, *host)
		if err != nil {
			return err
		}
		if err := json.NewEncoder(stdout).Encode(response); err != nil {
			return err
		}
		for _, h := range response.Hosts {
			if h.Error != nil || h.Status == "degraded" {
				return &ExitError{Code: 2, Err: fmt.Errorf("some hosts or servers are unavailable; see JSON errors")}
			}
		}
	case "capture":
		response, err := client.Capture(ctx, ids, lines)
		if err != nil {
			return err
		}
		if err := json.NewEncoder(stdout).Encode(response); err != nil {
			return err
		}
		for _, capture := range response.Captures {
			if capture.Error != nil {
				return &ExitError{Code: 2, Err: fmt.Errorf("some captures failed; see JSON errors")}
			}
		}
	case "attach":
		prefix, err := cfg.PrefixByte()
		if err != nil {
			return err
		}
		return app.RunAgent(ctx, attach.Options{SSH: opt, Prefix: prefix}, client, ids[0], *initiatingClient)
	}
	return nil
}
