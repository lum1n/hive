package execx

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBoundedOutput(t *testing.T) {
	for _, script := range []string{"printf 123456789", "printf 123456789 >&2", "while :; do printf 123456789; done"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		started := time.Now()
		result := Bounded(4, 4)(ctx, "sh", "-c", script)
		cancel()
		if !errors.Is(result.Err, ErrOutputLimit) || len(result.Stdout) > 4 || len(result.Stderr) > 4 {
			t.Fatal("output limit not enforced")
		}
		if time.Since(started) >= time.Second {
			t.Fatal("output limit did not cancel the command promptly")
		}
	}
	result := Bounded(20, 20)(context.Background(), "sh", "-c", "printf ok")
	if result.Err != nil || string(result.Stdout) != "ok" {
		t.Fatal("normal output changed")
	}
}
