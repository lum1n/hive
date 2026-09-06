package execx

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
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

func ExitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
