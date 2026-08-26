package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestConfigRoundTripPreservesAgentIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("TRAILWIRE_DATABASE", filepath.Join(t.TempDir(), "trailwire.db"))
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	agent, created, err := cfg.EnsureAgent("codex", "codex@test")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("new agent was not reported as created")
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	again, created, err := reloaded.EnsureAgent("codex", "ignored")
	if err != nil {
		t.Fatal(err)
	}
	if created || again != agent {
		t.Fatalf("agent changed: %#v to %#v", agent, again)
	}
}

func TestMessageTTLIsHumanConfiguredAndBounded(t *testing.T) {
	cfg := &Config{}
	if err := cfg.SetMessageTTL(24 * time.Hour); err != nil {
		t.Fatal(err)
	}
	if got, err := cfg.MessageTTLDuration(); err != nil || got != 24*time.Hour {
		t.Fatalf("MessageTTLDuration() = %s, %v", got, err)
	}
	if err := cfg.SetMessageTTL(MaximumMessageTTL + time.Hour); err == nil {
		t.Fatal("TTL above the maximum was accepted")
	}
	if err := cfg.SetMessageTTL(MinimumMessageTTL - time.Minute); err == nil {
		t.Fatal("TTL below the minimum was accepted")
	}
}
