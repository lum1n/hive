package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
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

const groupHelp = `Usage: hive agents [flags]
       hive agents <command> [flags]

Open the interactive agent picker across configured local and SSH hosts.
Type to filter; arrows or ctrl-j/k move; enter attaches to the exact pane.
Ctrl-r refreshes, esc clears the filter or exits, and ctrl-c quits.
Ctrl-space detaches back to the picker without stopping the agent.
Use --json for a headless inventory instead.

Commands:
  capabilities  Show the API version, supported agents, and limits
  list          Open the picker, or discover agent panes with --json
  capture       Fetch bounded previews (--json and --id required)
  attach        Attach to the exact agent pane (--id and a terminal required)

Examples:
  hive agents
  hive agents --host devbox
  hive agents --json
  hive agents capabilities --json
  hive agents list --host local --json
  hive agents capture --id '<reference>' --lines 200 --json
  hive agents attach --id '<reference>'

Use hive agents <command> --help for command flags and examples.
Both -h and --help work at every command level.
Help never loads configuration, opens the picker, or contacts hosts.
`

var commandHelp = map[string]struct{ usage, description, examples string }{
	"capabilities": {"hive agents capabilities --json",
		"Show versioned API capabilities without reading configuration or contacting hosts.",
		"  hive agents capabilities --json\n"},
	"list": {"hive agents list [--json] [--host ID]",
		"Open the interactive agent picker, or print versioned metadata with --json.\nExit 2 still emits complete JSON when a host or server is unavailable.",
		"  hive agents list\n  hive agents list --json\n  hive agents list --host local --timeout 30s --json\n"},
	"capture": {"hive agents capture --json --id REF [--id REF ...] [--lines 200]",
		"Capture up to 16 agent previews, batched by host, with 1-500 lines and at most 64 KiB per preview.\nUse the opaque id from list; failed targets remain in the JSON response (exit 2).",
		"  hive agents capture --id '<reference>' --lines 200 --json\n  hive agents capture --id '<first>' --id '<second>' --json\n"},
	"attach": {"hive agents attach --id REF [--client CLIENT]",
		"Attach to the exact agent pane in a terminal, or switch the initiating same-server client.\nReferences survive renames but not server restarts. Ctrl-space detaches without stopping the agent.",
		"  hive agents attach --id '<reference>'\n  hive agents attach --id '<reference>' --client /dev/ttys001\n"},
}

func WriteHelp(w io.Writer, flags *flag.FlagSet, usage, examples string) error {
	var text bytes.Buffer
	text.WriteString(usage)
	if flags != nil {
		text.WriteString("\nFlags:\n")
		output := flags.Output()
		flags.SetOutput(&text)
		flags.PrintDefaults()
		flags.SetOutput(output)
		text.WriteString("  -h, --help\n\tshow usage and examples and exit\n")
	}
	text.WriteString(examples)
	_, err := text.WriteTo(w)
	return err
}

func Run(ctx context.Context, args []string, configPath string, stdout, stderr io.Writer) error {
	command := "list"
	group := len(args) == 0 || strings.HasPrefix(args[0], "-")
	if !group {
		command, args = args[0], args[1:]
	}
	details, ok := commandHelp[command]
	if !ok {
		return fmt.Errorf("unknown agents command; use hive agents --help")
	}
	flags := flag.NewFlagSet("hive agents "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "print protocol-versioned JSON")
	path := flags.String("config", configPath, "Hive config file")
	timeout := flags.Duration("timeout", 12*time.Second, "per-host operation deadline (maximum 60s)")
	var host, initiatingClient *string
	var ids references
	lines := 200
	noWatcher := false
	switch command {
	case "list":
		flags.BoolVar(&noWatcher, "no-watcher", false, "disable optional agent-watcher state lookups")
		host = flags.String("host", "", "only this configured host")
		initiatingClient = flags.String("client", "", "initiating local tmux client for interactive attachment")
	case "capture":
		flags.BoolVar(&noWatcher, "no-watcher", false, "disable optional agent-watcher state lookups")
		flags.Var(&ids, "id", "agent reference; repeat for a batch")
		flags.IntVar(&lines, "lines", 200, "maximum captured rows")
	case "attach":
		flags.Var(&ids, "id", "agent reference")
		initiatingClient = flags.String("client", "", "initiating local tmux client")
	}
	var helpErr error
	flags.Usage = func() {
		if group {
			helpErr = WriteHelp(stderr, flags, groupHelp, "")
			return
		}
		helpErr = WriteHelp(stderr, flags, "Usage: "+details.usage+"\n\n"+details.description+"\n",
			"\nExamples:\n"+details.examples)
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return helpErr
		}
		return err
	}
	if len(flags.Args()) != 0 || *timeout <= 0 || *timeout > 60*time.Second {
		return fmt.Errorf("unexpected arguments or invalid timeout")
	}
	if command == "attach" && (*jsonOutput || len(ids) != 1) {
		return fmt.Errorf("attach needs exactly one --id and does not support --json")
	}
	if command != "attach" && command != "list" && !*jsonOutput {
		return fmt.Errorf("headless agent commands require --json")
	}
	if command == "list" && !*jsonOutput &&
		(!term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd()))) {
		return fmt.Errorf("agent picker needs a terminal; use hive agents list --json")
	}
	if command == "capabilities" {
		return json.NewEncoder(stdout).Encode(struct {
			Version      int      `json:"version"`
			Commands     []string `json:"commands"`
			Kinds        []string `json:"kinds"`
			MaxTargets   int      `json:"max_targets"`
			MaxLines     int      `json:"max_lines"`
			MaxBytes     int      `json:"max_bytes"`
			StateSources []string `json:"state_sources"`
		}{agents.Version, []string{"list", "capture", "attach"}, agents.Kinds,
			agents.MaxTargets, agents.MaxLines, agents.MaxBytes, []string{"heuristic", "shared"}})
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
	if noWatcher {
		cfg.AgentWatcher = "off"
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
		if !*jsonOutput {
			if *host != "" {
				selected, _ := cfg.Host(*host)
				client.Config.Hosts = []config.Host{selected}
			}
			return app.RunAgentPicker(ctx, client, *initiatingClient)
		}
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
