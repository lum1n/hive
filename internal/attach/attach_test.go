package attach

import (
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	t.Parallel()
	if Backoff(0) != time.Second {
		t.Fatal(Backoff(0))
	}
	if Backoff(1) != 2*time.Second {
		t.Fatal(Backoff(1))
	}
	if Backoff(10) != 30*time.Second {
		t.Fatal(Backoff(10))
	}
}

func TestIndexByte(t *testing.T) {
	t.Parallel()
	if indexByte([]byte("abc"), 0) != -1 {
		t.Fatal("missing")
	}
	if indexByte([]byte{'a', 0, 'b'}, 0) != 1 {
		t.Fatal("ctrl-space")
	}
}
