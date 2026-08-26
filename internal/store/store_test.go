package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "trailwire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func register(t *testing.T, store *Store, id, harness string) {
	t.Helper()
	if err := store.RegisterAgent(context.Background(), Agent{ID: id, Harness: harness, Name: harness}); err != nil {
		t.Fatal(err)
	}
}

func TestRepoMessagesReachActivePeersOnce(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	register(t, store, "claude-id", "claude")
	register(t, store, "codex-id", "codex")
	register(t, store, "cursor-id", "cursor")

	for _, presence := range []struct{ agent, session, repo string }{
		{"claude-id", "claude-session", "github.com/acme/widget"},
		{"codex-id", "codex-session", "github.com/acme/widget"},
		{"cursor-id", "cursor-session", "github.com/acme/other"},
	} {
		if err := store.TouchPresence(ctx, presence.agent, presence.session, presence.repo); err != nil {
			t.Fatal(err)
		}
	}

	_, recipients, err := store.Send(ctx, SendRequest{
		SenderID: "claude-id", TargetKind: "repo", TargetID: "github.com/acme/widget", Body: "I am changing the schema",
	})
	if err != nil {
		t.Fatal(err)
	}
	if recipients != 1 {
		t.Fatalf("recipients = %d, want 1", recipients)
	}

	messages, err := store.ClaimInbox(ctx, "codex-id", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Body != "I am changing the schema" {
		t.Fatalf("messages = %#v", messages)
	}
	second, err := store.ClaimInbox(ctx, "codex-id", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("second claim = %#v, want none", second)
	}
	cursor, err := store.ClaimInbox(ctx, "cursor-id", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(cursor) != 0 {
		t.Fatalf("other repository received messages: %#v", cursor)
	}
}

func TestChannelsAreIndependentFromRepositories(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	register(t, store, "claude-id", "claude")
	register(t, store, "cursor-id", "cursor")
	if err := store.JoinChannel(ctx, "claude-id", "#architecture"); err != nil {
		t.Fatal(err)
	}
	if err := store.JoinChannel(ctx, "cursor-id", "architecture"); err != nil {
		t.Fatal(err)
	}

	_, recipients, err := store.Send(ctx, SendRequest{
		SenderID: "claude-id", TargetKind: "channel", TargetID: "#architecture", Body: "The interface is changing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if recipients != 1 {
		t.Fatalf("recipients = %d, want 1", recipients)
	}
	messages, err := store.ClaimInbox(ctx, "cursor-id", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].TargetID != "architecture" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestDirectMessagesOnlyReachTarget(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	register(t, store, "claude-id", "claude")
	register(t, store, "codex-id", "codex")
	register(t, store, "cursor-id", "cursor")

	_, recipients, err := store.Send(ctx, SendRequest{
		SenderID: "claude-id", TargetKind: "agent", TargetID: "codex-id", Body: "Please keep that API stable",
	})
	if err != nil {
		t.Fatal(err)
	}
	if recipients != 1 {
		t.Fatalf("recipients = %d, want 1", recipients)
	}
	for id, want := range map[string]int{"codex-id": 1, "cursor-id": 0} {
		messages, err := store.ClaimInbox(ctx, id, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) != want {
			t.Fatalf("%s received %d messages, want %d", id, len(messages), want)
		}
	}
}

func TestExpiredIntentIsNotActive(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	register(t, store, "claude-id", "claude")
	register(t, store, "codex-id", "codex")
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	if err := store.SetIntent(ctx, Intent{
		AgentID: "claude-id", RepoID: "github.com/acme/widget", Summary: "Changing migrations", Paths: []string{"db/"}, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	intents, err := store.ActiveIntents(ctx, "github.com/acme/widget", "codex-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 1 || len(intents[0].Paths) != 1 {
		t.Fatalf("intents = %#v", intents)
	}

	store.now = func() time.Time { return now.Add(2 * time.Hour) }
	intents, err = store.ActiveIntents(ctx, "github.com/acme/widget", "codex-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 0 {
		t.Fatalf("expired intents = %#v", intents)
	}
}
