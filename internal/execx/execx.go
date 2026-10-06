package execx

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

// Result is a finished command.
type Result struct {
	Stdout []byte
	Stderr []byte
	Err    error
}

func (r Result) Combined() string {
	out := string(r.Stdout)
	if len(r.Stderr) > 0 {
		if out != "" {
			out += "\n"
		}
		out += string(r.Stderr)
	}
	return out
}

func (r Result) ErrText() string {
	if len(r.Stderr) > 0 {
		return string(r.Stderr)
	}
	if r.Err != nil {
		return r.Err.Error()
	}
	return string(r.Stdout)
}

// Runner runs a process. Tests replace this.
type Runner func(ctx context.Context, name string, args ...string) Result

func Default(ctx context.Context, name string, args ...string) Result {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Err: err}
}

var ErrOutputLimit = errors.New("command output limit exceeded")

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if len(p) <= remaining {
		return b.buffer.Write(p)
	}
	n, _ := b.buffer.Write(p[:remaining])
	b.exceeded = true
	b.cancel()
	return n, ErrOutputLimit
}

func Bounded(stdoutLimit, stderrLimit int) Runner {
	return func(ctx context.Context, name string, args ...string) Result {
		if stdoutLimit < 1 || stderrLimit < 1 {
			return Result{Err: errors.New("invalid command output limit")}
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.WaitDelay = 200 * time.Millisecond
		stdout := limitedBuffer{limit: stdoutLimit, cancel: cancel}
		stderr := limitedBuffer{limit: stderrLimit, cancel: cancel}
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if stdout.exceeded || stderr.exceeded {
			err = ErrOutputLimit
		}
		return Result{Stdout: stdout.buffer.Bytes(), Stderr: stderr.buffer.Bytes(), Err: err}
	}
}

func ExitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
