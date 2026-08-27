package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theoutdoorprogrammer/trailwire/internal/config"
	"github.com/theoutdoorprogrammer/trailwire/internal/store"
)

func TestVersionOneIdentityInboxAndMembershipMigrateToFirstSession(t *testing.T) {
	ctx := context.Background()
	temp := t.TempDir()
	databasePath := filepath.Join(temp, "trailwire.db")
	configPath := filepath.Join(temp, "config.json")
	legacyCodexID := "11111111-1111-4111-8111-111111111111"
	legacyClaudeID := "22222222-2222-4222-8222-222222222222"
	legacyConfig := fmt.Sprintf(`{
  "version": 1,
  "database": %q,
  "message_ttl": "168h0m0s",
  "agents": {
    "codex": {"id": %q, "name": "codex@test"},
    "claude": {"id": %q, "name": "claude@test"}
  }
}`, databasePath, legacyCodexID, legacyClaudeID)
	if err := os.WriteFile(configPath, []byte(legacyConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []store.Agent{
		{ID: legacyCodexID, Harness: "codex", Name: "codex@test"},
		{ID: legacyClaudeID, Harness: "claude", Name: "claude@test"},
	} {
		if err := database.RegisterAgent(ctx, agent); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.CreateChannel(ctx, "architecture"); err != nil {
		t.Fatal(err)
	}
	if err := database.JoinChannel(ctx, legacyCodexID, "architecture"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.Send(ctx, store.SendRequest{
		SenderID: legacyClaudeID, TargetKind: "agent", TargetID: legacyCodexID, Body: "legacy unread message",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	first, err := Open(ctx, Options{ConfigPath: configPath, Harness: "codex", NativeSessionID: "codex-thread-one", CWD: temp})
	if err != nil {
		t.Fatal(err)
	}
	if first.Agent.ID != legacyCodexID {
		t.Fatalf("first v1 session id = %q, want legacy id", first.Agent.ID)
	}
	events, err := first.Store.ClaimInbox(ctx, first.Agent.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Body != "legacy unread message" {
		t.Fatalf("migrated inbox = %#v", events)
	}
	channels, err := first.Store.Channels(ctx, first.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 1 || channels[0].Name != "architecture" || channels[0].Forced {
		t.Fatalf("migrated channels = %#v", channels)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	resumed, err := Open(ctx, Options{ConfigPath: configPath, Harness: "codex", NativeSessionID: "codex-thread-one", CWD: temp})
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	second, err := Open(ctx, Options{ConfigPath: configPath, Harness: "codex", NativeSessionID: "codex-thread-two", CWD: temp})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if resumed.Agent.ID != legacyCodexID || second.Agent.ID == legacyCodexID || second.Agent.ID == resumed.Agent.ID {
		t.Fatalf("resumed and concurrent identities = %#v and %#v", resumed.Agent, second.Agent)
	}

	migrated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(migrated), `"version": 2`) || !strings.Contains(string(migrated), `"installation_id"`) {
		t.Fatalf("config was not migrated: %s", migrated)
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.NeedsSave() {
		t.Fatal("migrated config still needs saving")
	}
}
