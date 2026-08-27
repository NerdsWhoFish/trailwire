package config

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestVersionOneConfigMigratesWithoutChangingLegacyAgents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := `{
  "version": 1,
  "database": "/tmp/trailwire.db",
  "message_ttl": "168h0m0s",
  "agents": {
    "human": {"id": "11111111-1111-4111-8111-111111111111", "name": "human@test"},
    "codex": {"id": "22222222-2222-4222-8222-222222222222", "name": "codex@test"}
  }
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != currentVersion || !cfg.NeedsSave() {
		t.Fatalf("migration state = version %d, dirty %t", cfg.Version, cfg.NeedsSave())
	}
	if cfg.InstallationID != cfg.Agents["human"].ID {
		t.Fatalf("installation id = %q, want legacy human id", cfg.InstallationID)
	}
	if cfg.Agents["codex"].ID != "22222222-2222-4222-8222-222222222222" {
		t.Fatal("legacy Codex identity changed")
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.NeedsSave() {
		t.Fatal("saved config still requires migration")
	}
}

func TestSessionAgentsAreStableAndDistinct(t *testing.T) {
	cfg := &Config{
		InstallationID: "11111111-1111-4111-8111-111111111111",
		Agents: map[string]Agent{
			"codex": {ID: "22222222-2222-4222-8222-222222222222", Name: "codex@test"},
		},
	}
	first, err := cfg.SessionAgent("codex", "thread-one")
	if err != nil {
		t.Fatal(err)
	}
	again, err := cfg.SessionAgent("codex", "thread-one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := cfg.SessionAgent("codex", "thread-two")
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Fatalf("resumed session changed identity: %#v to %#v", first, again)
	}
	if first.ID == second.ID || first.Name == second.Name {
		t.Fatalf("concurrent sessions collapsed: %#v and %#v", first, second)
	}
}

func TestForcedChannelsAreNormalized(t *testing.T) {
	cfg := &Config{}
	if err := cfg.SetForcedChannels([]string{"#Release", "architecture", "release"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"architecture", "release"}
	if !reflect.DeepEqual(cfg.ForcedChannels, want) {
		t.Fatalf("forced channels = %#v, want %#v", cfg.ForcedChannels, want)
	}
	if err := cfg.SetForcedChannels([]string{"#"}); err == nil {
		t.Fatal("empty channel name was accepted")
	}
}
