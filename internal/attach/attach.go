package attach

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/execx"
	"github.com/lum1n/hive/internal/sshx"
	"github.com/lum1n/hive/internal/tmux"
	"github.com/lum1n/hive/internal/workspace"
	"golang.org/x/term"
)

type Outcome int

const (
	OutcomeDetached Outcome = iota
	OutcomeExited
	OutcomeDisconnected
	OutcomeGone
)

func (o Outcome) String() string {
	switch o {
	case OutcomeDetached:
		return "detached"
	case OutcomeExited:
		return "exited"
	case OutcomeGone:
		return "gone"
	default:
		return "disconnected"
	}
}

type Options struct {
	SSH    sshx.Options
	Prefix byte
	Runner execx.Runner
}

func HasSession(ctx context.Context, opt Options, host config.Host, session string) error {
	run := opt.Runner
	if run == nil {
		run = execx.Default
	}
	name, args := hasInvocation(opt.SSH, host, session)
	res := run(ctx, name, args...)
	if res.Err == nil {
		return nil
	}
	msg := strings.TrimSpace(res.ErrText())
	if tmux.MissingServer(msg) || execx.ExitCode(res.Err) == 1 {
		return errGone
	}
	return fmt.Errorf("%s", compact(msg))
}

func Create(ctx context.Context, opt Options, host config.Host, session string) error {
	run := opt.Runner
	if run == nil {
		run = execx.Default
	}
	name, args := createInvocation(opt.SSH, host, session)
	res := run(ctx, name, args...)
	if res.Err != nil {
		return fmt.Errorf("create %s: %s", session, compact(res.ErrText()))
	}
	return nil
}

func Kill(ctx context.Context, opt Options, host config.Host, session string) error {
	run := opt.Runner
	if run == nil {
		run = execx.Default
	}
	name, args := killInvocation(opt.SSH, host, session)
	res := run(ctx, name, args...)
	if res.Err != nil && !tmux.MissingServer(res.ErrText()) {
		return fmt.Errorf("kill %s: %s", session, compact(res.ErrText()))
	}
	return nil
}

func Rename(ctx context.Context, opt Options, host config.Host, from, to string) error {
	run := opt.Runner
	if run == nil {
		run = execx.Default
	}
	name, args := renameInvocation(opt.SSH, host, from, to)
	res := run(ctx, name, args...)
	if res.Err != nil {
		return fmt.Errorf("rename %s: %s", from, compact(res.ErrText()))
	}
	return nil
}

var errGone = errors.New("session gone")

func IsGone(err error) bool {
	return errors.Is(err, errGone)
}

func Session(ctx context.Context, opt Options, host config.Host, id workspace.ID) (Outcome, error) {
	if err := HasSession(ctx, opt, host, id.Session); err != nil {
		if IsGone(err) {
			return OutcomeGone, nil
		}
		return OutcomeDisconnected, err
	}
	return runPTY(ctx, opt, host, id)
}

func runPTY(ctx context.Context, opt Options, host config.Host, id workspace.ID) (Outcome, error) {
	cmd := attachCmd(opt.SSH, host, id.Session)
	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		cols, rows = 80, 24
	}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return OutcomeDisconnected, fmt.Errorf("start attach: %w", err)
	}
	defer func() { _ = ptmx.Close() }()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	defer signal.Stop(ch)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch:
				if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
					_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)})
				}
			}
		}
	}()

	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		_ = cmd.Process.Kill()
		return OutcomeDisconnected, fmt.Errorf("raw mode: %w", err)
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), old) }()

	var stolen atomic.Bool
	prefixHit := make(chan struct{})
	go func() {
		_, _ = io.Copy(os.Stdout, ptmx)
	}()
	go func() {
		_ = copyInput(ptmx, opt.Prefix, prefixHit, &stolen)
	}()

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		stolen.Store(true)
		_ = cmd.Process.Kill()
		return OutcomeDetached, nil
	case <-prefixHit:
		_ = cmd.Process.Kill()
		return OutcomeDetached, nil
	case err := <-waitErr:
		if stolen.Load() {
			return OutcomeDetached, nil
		}
		if err == nil {
			return OutcomeExited, nil
		}
		if isGoneMessage(err.Error()) {
			return OutcomeGone, nil
		}
		return OutcomeDisconnected, err
	}
}

func copyInput(ptmx *os.File, steal byte, prefixHit chan struct{}, stolen *atomic.Bool) error {
	buf := make([]byte, 1024)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			out := buf[:n]
			if i := indexByte(out, steal); i >= 0 {
				if i > 0 {
					if _, werr := ptmx.Write(out[:i]); werr != nil {
						return werr
					}
				}
				stolen.Store(true)
				close(prefixHit)
				return nil
			}
			if _, werr := ptmx.Write(out); werr != nil {
				return werr
			}
		}
		if err != nil {
			return err
		}
	}
}

func indexByte(b []byte, v byte) int {
	for i, c := range b {
		if c == v {
			return i
		}
	}
	return -1
}

func attachCmd(opt sshx.Options, host config.Host, session string) *exec.Cmd {
	remote := tmux.AttachCommand(host.TmuxBin(), host.Socket, session)
	if host.Local {
		return exec.Command(remote[0], remote[1:]...)
	}
	args := sshx.AttachArgs(opt, host.ID, host.Destination(), host.ControlPath, remote)
	return exec.Command("ssh", args...)
}

func hasInvocation(opt sshx.Options, host config.Host, session string) (string, []string) {
	remote := tmux.HasSession(host.TmuxBin(), host.Socket, session)
	if host.Local {
		return remote[0], remote[1:]
	}
	return "ssh", sshx.ExecArgs(opt, host.ID, host.Destination(), host.ControlPath, remote)
}

func createInvocation(opt sshx.Options, host config.Host, session string) (string, []string) {
	remote := tmux.NewSession(host.TmuxBin(), host.Socket, session)
	if host.Local {
		return remote[0], remote[1:]
	}
	return "ssh", sshx.ExecArgs(opt, host.ID, host.Destination(), host.ControlPath, remote)
}

func killInvocation(opt sshx.Options, host config.Host, session string) (string, []string) {
	remote := tmux.KillSession(host.TmuxBin(), host.Socket, session)
	if host.Local {
		return remote[0], remote[1:]
	}
	return "ssh", sshx.ExecArgs(opt, host.ID, host.Destination(), host.ControlPath, remote)
}

func renameInvocation(opt sshx.Options, host config.Host, from, to string) (string, []string) {
	remote := tmux.RenameSession(host.TmuxBin(), host.Socket, from, to)
	if host.Local {
		return remote[0], remote[1:]
	}
	return "ssh", sshx.ExecArgs(opt, host.ID, host.Destination(), host.ControlPath, remote)
}

func Backoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := time.Second << attempt
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}

func compact(message string) string {
	message = strings.TrimSpace(message)
	message = strings.ReplaceAll(message, "\n", " ")
	if len(message) > 200 {
		return message[:197] + "..."
	}
	return message
}

func isGoneMessage(message string) bool {
	return tmux.MissingServer(message)
}
