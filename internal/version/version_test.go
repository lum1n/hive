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

func TestApplyBuildInfoDirtyAndDateFormats(t *testing.T) {
	origV, origC, origD := Version, Commit, Date
	t.Cleanup(func() {
		Version, Commit, Date = origV, origC, origD
	})

	Version, Commit, Date = "0.3.1", "none", "unknown"
	applyBuildInfo(&debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "aabbccddeeff"},
		{Key: "vcs.modified", Value: "true"},
		{Key: "vcs.time", Value: "2026-08-29T14:00:00+02:00"},
	}}, true)
	if Commit != "aabbccddeeff-dirty" || Date != "2026-08-29T12:00:00Z" {
		t.Fatalf("Commit=%q Date=%q", Commit, Date)
	}

	Commit, Date = "none", "unknown"
	applyBuildInfo(&debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.time", Value: "yesterday"},
	}}, true)
	if Commit != "none" || Date != "yesterday" || String() != "0.3.1" {
		t.Fatalf("Commit=%q Date=%q String=%q", Commit, Date, String())
	}
}

func TestApplyBuildInfoKeepsLdflags(t *testing.T) {
	origV, origC, origD := Version, Commit, Date
	t.Cleanup(func() {
		Version, Commit, Date = origV, origC, origD
	})
	Version, Commit, Date = "1.2.0", "1234567890", "2026-10-01T00:00:00Z"

	info := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "aabbccddeeff"},
		{Key: "vcs.time", Value: "2026-08-29T12:00:00Z"},
	}}
	applyBuildInfo(info, true)
	applyBuildInfo(nil, true)
	applyBuildInfo(&debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}}, false)
	if Version != "1.2.0" || Commit != "1234567890" || Date != "2026-10-01T00:00:00Z" {
		t.Fatalf("ldflags overwritten: %q %q %q", Version, Commit, Date)
	}
	if String() != "1.2.0 (1234567)" {
		t.Fatalf("String=%q", String())
	}
	if want := "hive 1.2.0\ncommit: 1234567890\nbuilt:  2026-10-01T00:00:00Z"; Full() != want {
		t.Fatalf("Full=%q", Full())
	}
}
