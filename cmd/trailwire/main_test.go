package main

import (
	"runtime/debug"
	"strings"
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

func TestCLINativeSessionIDNeverInventsHarnessIdentity(t *testing.T) {
	for _, key := range []string{"TRAILWIRE_SESSION_ID", "CODEX_THREAD_ID", "CODEX_SESSION_ID"} {
		t.Setenv(key, "")
	}
	if _, err := cliNativeSessionID("codex"); err == nil || !strings.Contains(err.Error(), "session id is unavailable") {
		t.Fatalf("missing Codex identity error = %v", err)
	}
	t.Setenv("CODEX_THREAD_ID", "real-thread")
	if got, err := cliNativeSessionID("codex"); err != nil || got != "real-thread" {
		t.Fatalf("Codex identity = %q, %v", got, err)
	}
	if got, err := cliNativeSessionID("human"); err != nil || got != "cli" {
		t.Fatalf("human identity = %q, %v", got, err)
	}
}
