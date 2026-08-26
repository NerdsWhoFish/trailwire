package mcpserver

import (
	"context"
	"os"
	"path/filepath"
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

	codex, err := session.Open(ctx, session.Options{ConfigPath: configPath, Harness: "codex", CWD: cwd, RequireRepo: true})
	if err != nil {
		t.Fatal(err)
	}
	defer codex.Close()
	if err := codex.Touch(ctx, "codex-test"); err != nil {
		t.Fatal(err)
	}

	claude, err := session.Open(ctx, session.Options{ConfigPath: configPath, Harness: "claude", CWD: cwd, RequireRepo: true})
	if err != nil {
		t.Fatal(err)
	}
	defer claude.Close()

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
	if len(channels) != 1 || channels[0] != "architecture" {
		t.Fatalf("channels = %#v", channels)
	}
}
