package cache

import (
	"testing"
	"time"

	"github.com/lum1n/hive/internal/workspace"
)

func TestStoreRoundTrip(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	snap := HostSnapshot{
		Time:   time.Unix(1710000000, 0).UTC(),
		Status: StatusOnline,
		Sessions: []Session{
			{Name: "backend", Windows: 2},
		},
	}
	if err := store.SaveHost("devbox", snap); err != nil {
		t.Fatal(err)
	}
	got := store.LoadHost("devbox")
	if got.Status != StatusOnline || len(got.Sessions) != 1 || got.Sessions[0].Name != "backend" {
		t.Fatalf("%+v", got)
	}
	id := workspace.ID{Host: "devbox", Session: "backend"}
	if err := store.SetLastWorkspace(id); err != nil {
		t.Fatal(err)
	}
	last, ok := store.LastWorkspace()
	if !ok || last != id {
		t.Fatalf("last=%+v ok=%v", last, ok)
	}
}

func TestLoadMissing(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := store.LoadHost("nope")
	if got.Status != StatusConnecting {
		t.Fatalf("%+v", got)
	}
}
