package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveBuildVersionUsesModuleMetadataForGoInstall(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.1.1"},
	}
	version, commit := resolveBuildVersion("dev", "unknown", info)
	if version != "0.1.1" || commit != "unknown" {
		t.Fatalf("resolved %s (%s)", version, commit)
	}
}

func TestResolveBuildVersionPreservesLinkerValues(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v9.9.9"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "aaaaaaaaaaaaaaaa"},
		},
	}
	version, commit := resolveBuildVersion("1.2.3", "1234567", info)
	if version != "1.2.3" || commit != "1234567" {
		t.Fatalf("resolved %s (%s)", version, commit)
	}
}

func TestResolveBuildVersionUsesVCSRevisionForLocalBuilds(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abcdef0123456789"},
		},
	}
	version, commit := resolveBuildVersion("dev", "unknown", info)
	if version != "dev" || commit != "abcdef0" {
		t.Fatalf("resolved %s (%s)", version, commit)
	}
}
