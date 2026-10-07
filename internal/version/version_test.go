package version

import (
	"runtime/debug"
	"testing"
)

func TestModuleVersion(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		"(devel)":           "",
		"v0.3.1":            "0.3.1",
		"0.3.1":             "0.3.1",
		"v0.3.1-4-gdeadbee": "0.3.1-4-gdeadbee",
	}
	for in, want := range cases {
		if got := moduleVersion(in); got != want {
			t.Errorf("moduleVersion(%q)=%q want %q", in, got, want)
		}
	}
}

func TestApplyBuildInfoFromGoInstall(t *testing.T) {
	origV, origC, origD := Version, Commit, Date
	t.Cleanup(func() {
		Version, Commit, Date = origV, origC, origD
	})
	Version, Commit, Date = "dev", "none", "unknown"

	applyBuildInfo(&debug.BuildInfo{
		Main: debug.Module{Version: "v0.3.1"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "aabbccddeeff"},
			{Key: "vcs.time", Value: "2026-08-29T12:00:00Z"},
		},
	}, true)

	if Version != "0.3.1" {
		t.Fatalf("Version=%q", Version)
	}
	if Commit != "aabbccddeeff" {
		t.Fatalf("Commit=%q", Commit)
	}
	if Date != "2026-08-29T12:00:00Z" {
		t.Fatalf("Date=%q", Date)
	}
	if String() != "0.3.1 (aabbccd)" {
		t.Fatalf("String=%q", String())
	}
}

func TestApplyBuildInfoDevelKeepsDefault(t *testing.T) {
	origV, origC, origD := Version, Commit, Date
	t.Cleanup(func() {
		Version, Commit, Date = origV, origC, origD
	})
	Version, Commit, Date = "0.3.1", "none", "unknown"

	applyBuildInfo(&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true)
	if Version != "0.3.1" {
		t.Fatalf("Version=%q", Version)
	}
}
