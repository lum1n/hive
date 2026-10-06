package attach

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lum1n/hive/internal/agents"
	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/execx"
	"github.com/lum1n/hive/internal/sshx"
	"github.com/lum1n/hive/internal/tmux"
)

func Agent(ctx context.Context, opt Options, client agents.Client, id, initiatingClient string) (Outcome, error) {
	host, ref, err := client.Resolve(ctx, id)
	if err != nil {
		var failure *agents.Failure
		if errors.As(err, &failure) && (failure.Code == "gone" || failure.Code == "stale") {
			return OutcomeGone, err
		}
		return OutcomeDisconnected, err
	}
	script, err := client.TargetScript(host, ref)
	if err != nil {
		return OutcomeGone, err
	}
	if host.Local && tmux.Inside() {
		socket := strings.SplitN(os.Getenv("TMUX"), ",", 2)[0]
		result := agentRun(ctx, opt, "tmux", "-S", socket, "display-message", "-p", "#{socket_path}")
		if result.Err != nil {
			return OutcomeDisconnected, fmt.Errorf("cannot resolve initiating tmux server")
		}
		currentSocket, err := filepath.EvalSymlinks(strings.TrimSpace(string(result.Stdout)))
		if err != nil {
			return OutcomeDisconnected, fmt.Errorf("cannot resolve initiating tmux socket")
		}
		if currentSocket == ref.Socket {
			name, err := agentClient(ctx, opt, socket, initiatingClient)
			if err != nil {
				return OutcomeDisconnected, err
			}
			script += focusScript(ref, name)
			result = agentRun(ctx, opt, "sh", "-c", script)
			if result.Err != nil {
				return OutcomeGone, fmt.Errorf("agent changed before focus; rediscover agents")
			}
			if strings.TrimSpace(string(result.Stdout)) != "FOCUSED" {
				return OutcomeDisconnected, fmt.Errorf("agent focus did not complete")
			}
			return OutcomeSwitched, nil
		}
	}
	script += agentAttachScript(ref)
	cmd := agentAttachCommand(opt.SSH, host, script)
	if tmux.Inside() {
		out, err := withOuterPassthrough(ctx, opt, func() (Outcome, error) {
			return runPTYCommand(ctx, opt, cmd)
		})
		return agentAttachOutcome(out, err)
	}
	out, err := runPTYCommand(ctx, opt, cmd)
	return agentAttachOutcome(out, err)
}

func agentAttachOutcome(out Outcome, err error) (Outcome, error) {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		switch exit.ExitCode() {
		case 8:
			return OutcomeGone, &agents.Failure{Code: "stale", Message: "tmux server changed; rediscover agents"}
		case 255:
			return OutcomeDisconnected, &agents.Failure{Code: "offline", Message: "SSH connection lost"}
		}
	}
	return out, err
}

func agentRun(ctx context.Context, opt Options, name string, args ...string) execx.Result {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	run := opt.Runner
	if run == nil {
		run = execx.Bounded(8192, 8192)
	}
	return run(ctx, name, args...)
}

func agentClient(ctx context.Context, opt Options, socket, explicit string) (string, error) {
	result := agentRun(ctx, opt, "tmux", "-S", socket, "list-clients", "-F", "#{client_name}\t#{pane_id}")
	if result.Err != nil {
		return "", fmt.Errorf("cannot resolve initiating tmux client")
	}
	var candidates []string
	for line := range strings.SplitSeq(string(result.Stdout), "\n") {
		name, pane, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if explicit != "" && name == explicit {
			return name, nil
		}
		if explicit == "" && pane == os.Getenv("TMUX_PANE") {
			candidates = append(candidates, name)
		}
	}
	if explicit == "" && len(candidates) == 1 {
		return candidates[0], nil
	}
	return "", fmt.Errorf("supply --client with an unambiguous initiating tmux client")
}

func focusScript(ref agents.Reference, client string) string {
	action := `_action=$(printf ` + sshx.SingleQuote("select-window -t '%s'; select-pane -t '%s'; switch-client -c %s -t '%s'; display-message -p FOCUSED") +
		` "$_session:$_window" "$_target" ` +
		sshx.SingleQuote(tmuxCommandQuote(client)) + ` "$_session:$_window.$_target")`
	return nativeAgentAction(ref, action, false)
}

func agentAttachScript(ref agents.Reference) string {
	action := `_action=$(printf ` + sshx.SingleQuote("select-window -t '%s'; select-pane -t '%s'; attach-session -t '%s'") +
		` "$_session:$_window" "$_target" "$_session")`
	return nativeAgentAction(ref, action, true)
}

func nativeAgentAction(ref agents.Reference, action string, attach bool) string {
	flag := ""
	if attach {
		flag = "-u "
	}
	condition := "#{==:#{pid}:#{start_time}," + ref.Generation + "}"
	// The condition and action execute in one server command queue, not across
	// two connections that could accidentally address a restarted server.
	return "\n" + action + `
exec "$HIVE_TMUX_BIN" ` + flag + `-S "$_sock" if-shell -F -t "$_target" ` +
		sshx.SingleQuote(condition) + ` "$_action" 'run-shell "exit 8"'
`
}

func tmuxCommandQuote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, ";", `\;`).Replace(value) + `"`
}

func agentAttachCommand(opt sshx.Options, host config.Host, script string) *exec.Cmd {
	var cmd *exec.Cmd
	if host.Local {
		cmd = exec.Command("sh", "-c", script)
	} else {
		cmd = exec.Command("ssh", sshx.AttachArgs(opt, host.ID, host.Destination(), host.ControlPath,
			[]string{"sh", "-c", script})...)
	}
	cmd.Env = dropEnv(os.Environ(), "TMUX")
	return cmd
}
