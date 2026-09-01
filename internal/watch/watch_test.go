package watch

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/theoutdoorprogrammer/trailwire/internal/store"
)

type fakeSource struct {
	messages []store.ObservedMessage
}

func (source fakeSource) ObserveMessages(_ context.Context, afterEventID int64, cutoff time.Time) ([]store.ObservedMessage, error) {
	var messages []store.ObservedMessage
	for _, message := range source.messages {
		if message.EventID > afterEventID && !message.MessageCreatedAt.Before(cutoff) {
			messages = append(messages, message)
		}
	}
	return messages, nil
}

func TestModelLoadsEveryScopeAndFiltersWithoutClaiming(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	messages := []store.ObservedMessage{
		{EventID: 1, ID: 1, EventKind: "created", SenderID: "claude", SenderName: "Claude", TargetKind: "repo", TargetID: "github.com/acme/widget", Body: "repo message", CreatedAt: now, MessageCreatedAt: now},
		{EventID: 2, ID: 2, EventKind: "created", SenderID: "codex", SenderName: "Codex", TargetKind: "channel", TargetID: "architecture", Body: "channel message", CreatedAt: now, MessageCreatedAt: now},
		{EventID: 3, ID: 3, EventKind: "created", SenderID: "cursor", SenderName: "Cursor", TargetKind: "agent", TargetID: "codex", TargetName: "Codex", Body: "direct message", CreatedAt: now, MessageCreatedAt: now},
	}
	subject := newModel(context.Background(), fakeSource{messages: messages}, Options{
		MessageTTL: time.Hour,
		Now:        func() time.Time { return now },
	})

	loaded := subject.Init()().(refreshMsg)
	updated, _ := subject.Update(loaded)
	subject = updated.(model)
	updated, _ = subject.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	subject = updated.(model)

	all := ansi.Strip(subject.View().Content)
	for _, body := range []string{"repo message", "channel message", "direct message"} {
		if !strings.Contains(all, body) {
			t.Fatalf("all scope omitted %q:\n%s", body, all)
		}
	}
	if !strings.Contains(all, "3 events  1 repos  1 channels  3 agents") {
		t.Fatalf("header did not summarize global history:\n%s", all)
	}

	subject.scope = scopeChannel
	subject.rebuildViewport()
	channels := ansi.Strip(subject.View().Content)
	if !strings.Contains(channels, "channel message") || strings.Contains(channels, "repo message") || strings.Contains(channels, "direct message") {
		t.Fatalf("channel filter rendered the wrong history:\n%s", channels)
	}
}

func TestApplyRefreshPrunesExpiredMessagesAndAdvancesCursor(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	subject := newModel(context.Background(), fakeSource{}, Options{MessageTTL: time.Hour, Now: func() time.Time { return now }})
	subject.messages = []store.ObservedMessage{
		{EventID: 1, MessageCreatedAt: now.Add(-2 * time.Hour)},
	}
	subject.applyRefresh(refreshMsg{
		cutoff: now.Add(-time.Hour),
		messages: []store.ObservedMessage{
			{EventID: 2, MessageCreatedAt: now},
		},
	})
	if len(subject.messages) != 1 || subject.messages[0].EventID != 2 || subject.cursor != 2 {
		t.Fatalf("refreshed model = %#v, cursor %d", subject.messages, subject.cursor)
	}
}

func TestSafeTextRemovesTerminalControlSequences(t *testing.T) {
	unsafe := "hello\x1b]8;;https://example.com\x1b\\click\x1b]8;;\x1b\\\x00\r\u202eworld"
	safe := safeText(unsafe)
	if strings.ContainsAny(safe, "\x1b\x00\r\u202e") {
		t.Fatalf("safe text retained terminal controls: %q", safe)
	}
	if !strings.Contains(safe, "hello") || !strings.Contains(safe, "world") {
		t.Fatalf("safe text dropped content: %q", safe)
	}
}
