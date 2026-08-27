package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/theoutdoorprogrammer/trailwire/internal/session"
)

func TestMCPRoutesMessagesAndProposesChannels(t *testing.T) {
	ctx := context.Background()
	temp := t.TempDir()
	t.Setenv("TRAILWIRE_DATA_DIR", filepath.Join(temp, "data"))
	configPath := filepath.Join(temp, "config.json")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	codex, err := session.Open(ctx, session.Options{ConfigPath: configPath, Harness: "codex", NativeSessionID: "codex-test", CWD: cwd, RequireRepo: true})
	if err != nil {
		t.Fatal(err)
	}
	defer codex.Close()
	if err := codex.Touch(ctx, "codex-test"); err != nil {
		t.Fatal(err)
	}

	claude, err := session.Open(ctx, session.Options{ConfigPath: configPath, Harness: "claude", NativeSessionID: "claude-test", CWD: cwd, RequireRepo: true})
	if err != nil {
		t.Fatal(err)
	}
	defer claude.Close()
	t.Setenv("TRAILWIRE_SESSION_ID", "claude-test")

	server := New(claude, "test")
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.MCP().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "trailwire_send", Arguments: map[string]any{"scope": "repo", "body": "I am changing the command API"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("send tool failed: %#v", result.Content)
	}
	events, err := codex.Store.ClaimInbox(ctx, codex.Agent.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Body != "I am changing the command API" {
		t.Fatalf("events = %#v", events)
	}
	second, err := codex.Store.ClaimInbox(ctx, codex.Agent.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("message replayed: %#v", second)
	}

	result, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "trailwire_propose_channel", Arguments: map[string]any{"name": "architecture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("propose channel failed: %#v", result.Content)
	}
	channels, err := claude.Store.Channels(ctx, claude.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 1 || channels[0].Name != "architecture" || channels[0].Forced {
		t.Fatalf("channels = %#v", channels)
	}
}

func TestRequestSessionIDReadsCodexTurnMetadata(t *testing.T) {
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{
		"x-codex-turn-metadata": map[string]any{
			"session_id": "codex-session", "thread_id": "codex-thread",
		},
	}}}
	if got := requestSessionID(request); got != "codex-thread" {
		t.Fatalf("session id = %q, want codex-thread", got)
	}
}

func TestMCPBindsCursorCallFromPreToolHookContext(t *testing.T) {
	ctx := context.Background()
	temp := t.TempDir()
	t.Setenv("TRAILWIRE_DATA_DIR", filepath.Join(temp, "data"))
	for _, key := range []string{"TRAILWIRE_SESSION_ID", "CURSOR_CONVERSATION_ID", "CURSOR_SESSION_ID"} {
		t.Setenv(key, "")
	}
	configPath := filepath.Join(temp, "config.json")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := session.Open(ctx, session.Options{
		ConfigPath: configPath, Harness: "claude", NativeSessionID: "claude-recipient", CWD: cwd, RequireRepo: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer recipient.Close()
	if err := recipient.Touch(ctx, "claude-recipient"); err != nil {
		t.Fatal(err)
	}
	cursor, err := session.Open(ctx, session.Options{ConfigPath: configPath, Harness: "cursor", CWD: cwd, RequireRepo: true})
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	arguments := []byte(`{"scope":"repo","body":"Cursor is changing the schema"}`)
	toolName, fingerprint, ok := session.ToolFingerprint("MCP:trailwire_send", arguments)
	if !ok {
		t.Fatal("Trailwire tool was not recognized")
	}
	if err := cursor.Store.RecordMCPCall(ctx, "cursor", "cursor-conversation", toolName, fingerprint, "cursor-call"); err != nil {
		t.Fatal(err)
	}

	server := New(cursor, "test")
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.MCP().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "cursor-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	var input map[string]any
	if err := json.Unmarshal(arguments, &input); err != nil {
		t.Fatal(err)
	}
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "trailwire_send", Arguments: input, Meta: mcp.Meta{"callId": "cursor-call"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("send tool failed: %#v", result.Content)
	}
	events, err := recipient.Store.ClaimInbox(ctx, recipient.Agent.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Body != "Cursor is changing the schema" {
		t.Fatalf("events = %#v", events)
	}
}

func TestToolDescriptionsTeachProactiveAndAutomaticCoordination(t *testing.T) {
	ctx := context.Background()
	temp := t.TempDir()
	t.Setenv("TRAILWIRE_DATA_DIR", filepath.Join(temp, "data"))
	t.Setenv("TRAILWIRE_SESSION_ID", "description-test")
	configPath := filepath.Join(temp, "config.json")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	active, err := session.Open(ctx, session.Options{ConfigPath: configPath, Harness: "codex", CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	server := New(active, "test")
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.MCP().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	tools, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	descriptions := make(map[string]string, len(tools.Tools))
	for _, tool := range tools.Tools {
		descriptions[tool.Name] = tool.Description
	}
	checks := map[string][]string{
		"trailwire_announce":    {"Proactively announce", "shared interfaces", "likely file overlap"},
		"trailwire_send":        {"once to each eligible recipient", "reply_to"},
		"trailwire_check_inbox": {"Manual recovery", "do not poll", "never blocks another recipient"},
		"trailwire_list_agents": {"multiple concurrent sessions", "exact id or full name"},
	}
	for name, fragments := range checks {
		for _, fragment := range fragments {
			if !strings.Contains(descriptions[name], fragment) {
				t.Errorf("%s description missing %q: %q", name, fragment, descriptions[name])
			}
		}
	}
}
