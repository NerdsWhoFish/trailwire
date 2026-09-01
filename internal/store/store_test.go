package store

import (
	"context"
	"path/filepath"
	"sync"
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

func TestConcurrentClaimsDeliverAnEventOnce(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "trailwire.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	register(t, first, "claude-id", "claude")
	register(t, first, "codex-id", "codex")
	if _, _, err := first.Send(ctx, SendRequest{
		SenderID: "claude-id", TargetKind: "agent", TargetID: "codex-id", Body: "claim me once",
	}); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan []Message, 2)
	errors := make(chan error, 2)
	var wait sync.WaitGroup
	for _, database := range []*Store{first, second} {
		wait.Add(1)
		go func(database *Store) {
			defer wait.Done()
			<-start
			messages, err := database.ClaimInbox(ctx, "codex-id", 50)
			results <- messages
			errors <- err
		}(database)
	}
	close(start)
	wait.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	total := 0
	for messages := range results {
		total += len(messages)
	}
	if total != 1 {
		t.Fatalf("concurrent claims delivered %d events, want 1", total)
	}
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

func TestRepoMessagesReachEveryConcurrentSessionOnce(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	register(t, store, "sender-id", "claude")
	register(t, store, "codex-b", "codex")
	register(t, store, "codex-c", "codex")
	for _, id := range []string{"sender-id", "codex-b", "codex-c"} {
		if err := store.TouchPresence(ctx, id, id+"-session", "github.com/acme/widget"); err != nil {
			t.Fatal(err)
		}
	}

	_, recipients, err := store.Send(ctx, SendRequest{
		SenderID: "sender-id", TargetKind: "repo", TargetID: "github.com/acme/widget", Body: "shared file is changing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if recipients != 2 {
		t.Fatalf("recipients = %d, want 2", recipients)
	}
	for _, id := range []string{"codex-b", "codex-c"} {
		first, err := store.ClaimInbox(ctx, id, 50)
		if err != nil {
			t.Fatal(err)
		}
		second, err := store.ClaimInbox(ctx, id, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(first) != 1 || len(second) != 0 {
			t.Fatalf("%s claims = %d then %d, want 1 then 0", id, len(first), len(second))
		}
	}
}

func TestSessionBindingAdoptsLegacyThenSeparatesAndResumes(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	register(t, store, "legacy-codex", "codex")
	firstCandidate := Agent{ID: "candidate-one", Harness: "codex", Name: "codex@test/one"}
	first, err := store.BindSession(ctx, "codex", "thread-one", firstCandidate, "legacy-codex")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "legacy-codex" {
		t.Fatalf("first session id = %q, want legacy identity", first.ID)
	}
	secondCandidate := Agent{ID: "candidate-two", Harness: "codex", Name: "codex@test/two"}
	second, err := store.BindSession(ctx, "codex", "thread-two", secondCandidate, "legacy-codex")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != "candidate-two" {
		t.Fatalf("second session id = %q, want distinct candidate", second.ID)
	}
	resumed, err := store.BindSession(ctx, "codex", "thread-one", Agent{ID: "wrong", Harness: "codex", Name: "wrong"}, "legacy-codex")
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID != first.ID {
		t.Fatalf("resumed identity = %q, want %q", resumed.ID, first.ID)
	}
}

func TestChannelsAreIndependentFromRepositories(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	register(t, store, "claude-id", "claude")
	register(t, store, "cursor-id", "cursor")
	if err := store.CreateChannel(ctx, "architecture"); err != nil {
		t.Fatal(err)
	}
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

func TestForcedChannelReachesEveryKnownSessionAndCannotBeLeft(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	for _, agent := range []struct{ id, harness string }{{"legacy", "claude"}, {"candidate-b", "codex"}, {"candidate-c", "codex"}} {
		register(t, store, agent.id, agent.harness)
	}
	claude, err := store.BindSession(ctx, "claude", "thread-a", Agent{ID: "new-a", Harness: "claude", Name: "claude/a"}, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	codexB, err := store.BindSession(ctx, "codex", "thread-b", Agent{ID: "new-b", Harness: "codex", Name: "codex/b"}, "")
	if err != nil {
		t.Fatal(err)
	}
	codexC, err := store.BindSession(ctx, "codex", "thread-c", Agent{ID: "new-c", Harness: "codex", Name: "codex/c"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SyncForcedChannels(ctx, []string{"architecture"}); err != nil {
		t.Fatal(err)
	}

	_, recipients, err := store.Send(ctx, SendRequest{
		SenderID: claude.ID, TargetKind: "channel", TargetID: "architecture", Body: "contract changed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if recipients != 2 {
		t.Fatalf("recipients = %d, want 2", recipients)
	}
	for _, agent := range []Agent{codexB, codexC} {
		events, err := store.ClaimInbox(ctx, agent.ID, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 {
			t.Fatalf("%s received %d events, want 1", agent.ID, len(events))
		}
	}
	channels, err := store.Channels(ctx, codexB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 1 || channels[0].Name != "architecture" || !channels[0].Forced {
		t.Fatalf("channels = %#v", channels)
	}
	if err := store.LeaveChannel(ctx, codexB.ID, "architecture"); err == nil {
		t.Fatal("agent left a mandatory channel")
	}
	if err := store.SyncForcedChannels(ctx, nil); err != nil {
		t.Fatal(err)
	}
	channels, err = store.Channels(ctx, codexB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 0 {
		t.Fatalf("removed policy still listed: %#v", channels)
	}
}

func TestMCPCallContextIsClaimedOnce(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.RecordMCPCall(ctx, "cursor", "conversation-1", "trailwire_send", "hash", "call-1"); err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimMCPCall(ctx, "cursor", "trailwire_send", "hash", "call-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.ClaimMCPCall(ctx, "cursor", "trailwire_send", "hash", "call-1")
	if err != nil {
		t.Fatal(err)
	}
	if first != "conversation-1" || second != "" {
		t.Fatalf("claims = %q then %q", first, second)
	}
}

func TestMessageModificationAndRecantAreDeliveredAsEvents(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	register(t, store, "claude-id", "claude")
	register(t, store, "codex-id", "codex")

	messageID, _, err := store.Send(ctx, SendRequest{
		SenderID: "claude-id", TargetKind: "agent", TargetID: "codex-id", Body: "I am changing api/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ModifyMessage(ctx, "claude-id", messageID, "I am changing api/v2"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecantMessage(ctx, "claude-id", messageID, "That work is no longer needed"); err != nil {
		t.Fatal(err)
	}

	events, err := store.ClaimInbox(ctx, "codex-id", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("received %d events, want 3", len(events))
	}
	wantKinds := []string{"created", "modified", "recanted"}
	for i, want := range wantKinds {
		if events[i].ID != messageID || events[i].EventKind != want {
			t.Errorf("event %d = %#v, want message %d kind %s", i, events[i], messageID, want)
		}
	}
	if _, err := store.ModifyMessage(ctx, "claude-id", messageID, "too late"); err == nil {
		t.Fatal("recanted message was modified")
	}
	if _, err := store.RecantMessage(ctx, "codex-id", messageID, "not mine"); err == nil {
		t.Fatal("non-sender recanted a message")
	}
}

func TestObserveMessagesReadsUnexpiredHistoryWithoutClaimingInbox(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t)
	register(t, database, "claude-id", "claude")
	register(t, database, "codex-id", "codex")
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	database.now = func() time.Time { return now }

	messageID, _, err := database.Send(ctx, SendRequest{
		SenderID: "claude-id", TargetKind: "agent", TargetID: "codex-id", Body: "Changing the storage contract",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ModifyMessage(ctx, "claude-id", messageID, "Keeping the storage contract stable"); err != nil {
		t.Fatal(err)
	}

	history, err := database.ObserveMessages(ctx, 0, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("observed %d events, want 2", len(history))
	}
	if history[0].SenderHarness != "claude" || history[0].TargetName != "codex" {
		t.Fatalf("observed identity = %#v", history[0])
	}
	if history[1].EventKind != "modified" || history[1].Body != "Keeping the storage contract stable" {
		t.Fatalf("observed revision = %#v", history[1])
	}

	next, err := database.ObserveMessages(ctx, history[0].EventID, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0].EventID != history[1].EventID {
		t.Fatalf("cursor result = %#v", next)
	}

	inbox, err := database.ClaimInbox(ctx, "codex-id", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 2 {
		t.Fatalf("observer claimed inbox events, received %d after observing", len(inbox))
	}

	database.now = func() time.Time { return now.Add(2 * time.Hour) }
	expired, err := database.ObserveMessages(ctx, 0, database.now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 0 {
		t.Fatalf("observed expired history = %#v", expired)
	}
}

func TestCleanupRemovesExpiredHistoryAndIntents(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	register(t, store, "claude-id", "claude")
	register(t, store, "codex-id", "codex")
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	messageID, _, err := store.Send(ctx, SendRequest{
		SenderID: "claude-id", TargetKind: "agent", TargetID: "codex-id", Body: "old message",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetIntent(ctx, Intent{
		AgentID: "claude-id", RepoID: "github.com/acme/widget", Summary: "old intent", ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	store.now = func() time.Time { return now.Add(8 * 24 * time.Hour) }
	result, err := store.Cleanup(ctx, store.now().Add(-7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.Messages != 1 || result.Intents != 1 {
		t.Fatalf("cleanup = %#v", result)
	}
	if _, err := store.ModifyMessage(ctx, "claude-id", messageID, "gone"); err == nil {
		t.Fatal("expired message history still exists")
	}
	events, err := store.ClaimInbox(ctx, "codex-id", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expired inbox events = %#v", events)
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

func TestResolveAgentRequiresExactIdentityWhenHarnessIsAmbiguous(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t)
	for _, agent := range []Agent{
		{ID: "codex-one", Harness: "codex", Name: "codex@test/one"},
		{ID: "codex-two", Harness: "codex", Name: "codex@test/two"},
	} {
		if err := database.RegisterAgent(ctx, agent); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := database.ResolveAgent(ctx, "codex"); err == nil {
		t.Fatal("ambiguous harness resolved without requiring an exact identity")
	}
	resolved, err := database.ResolveAgent(ctx, "codex@test/two")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != "codex-two" {
		t.Fatalf("resolved id = %q, want codex-two", resolved.ID)
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
