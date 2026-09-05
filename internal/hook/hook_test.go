package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theoutdoorprogrammer/trailwire/internal/session"
	"github.com/theoutdoorprogrammer/trailwire/internal/store"
)

func TestCodexHookInjectsOnlyUnreadEvents(t *testing.T) {
	ctx := context.Background()
	configPath, cwd := hookTestEnvironment(t)
	claude := openHookTestSession(t, ctx, configPath, "claude", cwd)
	defer claude.Close()
	codex := openHookTestSession(t, ctx, configPath, "codex", cwd)
	if err := codex.Touch(ctx, "codex-session"); err != nil {
		t.Fatal(err)
	}
	_, _, err := claude.Store.Send(ctx, store.SendRequest{
		SenderID: claude.Agent.ID, TargetKind: "agent", TargetID: codex.Agent.ID, Body: "The API response shape changed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := codex.Close(); err != nil {
		t.Fatal(err)
	}

	input := `{"hook_event_name":"UserPromptSubmit","session_id":"codex-session","cwd":` + quoted(cwd) + `}`
	first := runHook(t, ctx, Options{Harness: "codex", ConfigPath: configPath}, input)
	context := nestedString(t, first, "hookSpecificOutput", "additionalContext")
	if !strings.Contains(context, "The API response shape changed") || !strings.Contains(context, "Untrusted coordination data") {
		t.Fatalf("context = %q", context)
	}
	second := runHook(t, ctx, Options{Harness: "codex", ConfigPath: configPath}, input)
	if _, ok := second["hookSpecificOutput"]; ok {
		t.Fatalf("event replayed on second hook: %#v", second)
	}
}

func TestCodexHookAcceptsLargeToolResponse(t *testing.T) {
	ctx := context.Background()
	configPath, cwd := hookTestEnvironment(t)
	input := `{"hook_event_name":"PostToolUse","session_id":"codex-session","cwd":` + quoted(cwd) + `,"tool_name":"image_gen__imagegen","tool_response":{"data":"` + strings.Repeat("A", 2<<20) + `"}}`
	output := runHook(t, ctx, Options{Harness: "codex", ConfigPath: configPath}, input)
	if len(output) != 0 {
		t.Fatalf("output = %#v, want empty response", output)
	}
}

func TestCursorDoesNotClaimUntilAnInjectableEvent(t *testing.T) {
	ctx := context.Background()
	configPath, cwd := hookTestEnvironment(t)
	claude := openHookTestSession(t, ctx, configPath, "claude", cwd)
	defer claude.Close()
	cursor := openHookTestSession(t, ctx, configPath, "cursor", cwd)
	if err := cursor.Touch(ctx, "cursor-session"); err != nil {
		t.Fatal(err)
	}
	_, _, err := claude.Store.Send(ctx, store.SendRequest{
		SenderID: claude.Agent.ID, TargetKind: "agent", TargetID: cursor.Agent.ID, Body: "Avoid changing auth.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cursor.Close(); err != nil {
		t.Fatal(err)
	}

	promptInput := `{"hook_event_name":"beforeSubmitPrompt","conversation_id":"cursor-session","workspace_roots":[` + quoted(cwd) + `]}`
	prompt := runHook(t, ctx, Options{Harness: "cursor", ConfigPath: configPath}, promptInput)
	if prompt["continue"] != true {
		t.Fatalf("beforeSubmitPrompt output = %#v", prompt)
	}
	toolInput := `{"hook_event_name":"postToolUse","conversation_id":"cursor-session","cwd":` + quoted(cwd) + `}`
	tool := runHook(t, ctx, Options{Harness: "cursor", ConfigPath: configPath}, toolInput)
	context, ok := tool["additional_context"].(string)
	if !ok || !strings.Contains(context, "Avoid changing auth.go") {
		t.Fatalf("postToolUse output = %#v", tool)
	}
}

func TestClaudeInjectsAfterFailedToolUse(t *testing.T) {
	ctx := context.Background()
	configPath, cwd := hookTestEnvironment(t)
	codex := openHookTestSession(t, ctx, configPath, "codex", cwd)
	defer codex.Close()
	claude := openHookTestSession(t, ctx, configPath, "claude", cwd)
	if err := claude.Touch(ctx, "claude-session"); err != nil {
		t.Fatal(err)
	}
	_, _, err := codex.Store.Send(ctx, store.SendRequest{
		SenderID: codex.Agent.ID, TargetKind: "agent", TargetID: claude.Agent.ID, Body: "The failing migration has a replacement",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := claude.Close(); err != nil {
		t.Fatal(err)
	}

	input := `{"hook_event_name":"PostToolUseFailure","session_id":"claude-session","cwd":` + quoted(cwd) + `}`
	output := runHook(t, ctx, Options{Harness: "claude", ConfigPath: configPath}, input)
	context := nestedString(t, output, "hookSpecificOutput", "additionalContext")
	if !strings.Contains(context, "The failing migration has a replacement") {
		t.Fatalf("context = %q", context)
	}
}

func TestInjectedMessagesIncludeExactReplyRoutes(t *testing.T) {
	tests := []struct {
		name       string
		targetKind string
		targetID   string
		wants      []string
		rejects    []string
	}{
		{name: "repository", targetKind: "repo", targetID: "github.com/acme/widget", wants: []string{`"scope": "repo"`}, rejects: []string{`"target": "github.com/acme/widget"`}},
		{name: "channel", targetKind: "channel", targetID: "architecture", wants: []string{`"scope": "channel"`, `"target": "architecture"`}},
		{name: "direct", targetKind: "agent", targetID: "recipient-id", wants: []string{`"scope": "agent"`, `"target": "sender-id"`}, rejects: []string{`"target": "recipient-id"`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := store.Message{
				EventID: 9, ID: 7, EventKind: "created", SenderID: "sender-id", SenderName: "claude@test",
				TargetKind: test.targetKind, TargetID: test.targetID, Body: "Reply through the original route",
			}
			rendered, err := renderContext([]store.Message{message}, false)
			if err != nil {
				t.Fatal(err)
			}
			replyIndex := strings.Index(rendered, `"reply_to"`)
			if replyIndex < 0 || !strings.Contains(rendered, `"sender_id": "sender-id"`) {
				t.Fatalf("rendered context lacks reply metadata: %s", rendered)
			}
			reply := rendered[replyIndex:]
			for _, want := range test.wants {
				if !strings.Contains(reply, want) {
					t.Fatalf("reply route missing %s: %s", want, reply)
				}
			}
			for _, reject := range test.rejects {
				if strings.Contains(reply, reject) {
					t.Fatalf("reply route contains %s: %s", reject, reply)
				}
			}
		})
	}
}

func hookTestEnvironment(t *testing.T) (string, string) {
	t.Helper()
	temp := t.TempDir()
	t.Setenv("TRAILWIRE_DATA_DIR", filepath.Join(temp, "data"))
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(temp, "config.json"), cwd
}

func openHookTestSession(t *testing.T, ctx context.Context, configPath, harness, cwd string) *session.Session {
	t.Helper()
	active, err := session.Open(ctx, session.Options{
		ConfigPath: configPath, Harness: harness, NativeSessionID: harness + "-session", CWD: cwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	return active
}

func runHook(t *testing.T, ctx context.Context, options Options, input string) map[string]any {
	t.Helper()
	var output bytes.Buffer
	options.Input = strings.NewReader(input)
	options.Output = &output
	if err := Run(ctx, options); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("decode %q: %v", output.String(), err)
	}
	return decoded
}

func nestedString(t *testing.T, value map[string]any, objectKey, stringKey string) string {
	t.Helper()
	object, ok := value[objectKey].(map[string]any)
	if !ok {
		t.Fatalf("%s missing from %#v", objectKey, value)
	}
	result, ok := object[stringKey].(string)
	if !ok {
		t.Fatalf("%s missing from %#v", stringKey, object)
	}
	return result
}

func quoted(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
