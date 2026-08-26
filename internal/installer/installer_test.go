package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallConfiguresEveryHarnessAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "trailwire-config.json")
	t.Setenv("TRAILWIRE_DATA_DIR", filepath.Join(home, "data"))
	writeFixture(t, filepath.Join(home, ".claude", "settings.json"), `{
  "permissions": {"allow": ["Bash"]},
  "hooks": {"PreToolUse": [{"matcher":"Bash","hooks":[{"type":"command","command":"other-hook"}]}]}
}`)
	writeFixture(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"existing":{"command":"existing"}},"theme":"dark"}`)
	writeFixture(t, filepath.Join(home, ".codex", "hooks.json"), `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"command":"other-hook"}]}]}}`)
	writeFixture(t, filepath.Join(home, ".codex", "config.toml"), "model = \"gpt-test\"\n\n[mcp_servers.existing]\ncommand = \"existing\"\n")
	writeFixture(t, filepath.Join(home, ".cursor", "hooks.json"), `{"version":1,"hooks":{"preToolUse":[{"command":"other-hook"}]}}`)
	writeFixture(t, filepath.Join(home, ".cursor", "mcp.json"), `{"mcpServers":{"existing":{"command":"existing"}}}`)

	options := Options{
		HomeDir: home, BinaryPath: filepath.Join(home, "Trail Wire", "trailwire"), ConfigPath: configPath,
	}
	first, err := Install(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range first.Files {
		if !file.Changed {
			t.Errorf("first install left %s unchanged", file.Path)
		}
	}
	second, err := Install(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range second.Files {
		if file.Changed {
			t.Errorf("second install changed %s", file.Path)
		}
	}

	claudeSettings := readObject(t, filepath.Join(home, ".claude", "settings.json"))
	if _, ok := claudeSettings["permissions"]; !ok {
		t.Fatal("Claude settings lost unrelated permissions")
	}
	assertHookCount(t, claudeSettings, "PreToolUse", 2)
	claudeConfig := readObject(t, filepath.Join(home, ".claude.json"))
	assertMCPServers(t, claudeConfig)
	if claudeConfig["theme"] != "dark" {
		t.Fatal("Claude config lost unrelated fields")
	}
	cursorHooks := readObject(t, filepath.Join(home, ".cursor", "hooks.json"))
	assertHookCount(t, cursorHooks, "preToolUse", 2)
	assertMCPServers(t, readObject(t, filepath.Join(home, ".cursor", "mcp.json")))

	codexConfig, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(codexConfig)
	for _, want := range []string{"model = \"gpt-test\"", "[mcp_servers.existing]", "[mcp_servers.trailwire]", "# trailwire:start"} {
		if !strings.Contains(text, want) {
			t.Errorf("Codex config missing %q:\n%s", want, text)
		}
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config", "trailwire.json")
	t.Setenv("TRAILWIRE_DATA_DIR", filepath.Join(home, "data"))
	result, err := Install(Options{HomeDir: home, BinaryPath: "/usr/local/bin/trailwire", ConfigPath: configPath, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range result.Files {
		if !file.Changed {
			t.Errorf("dry run did not report %s", file.Path)
		}
		if _, err := os.Stat(file.Path); !os.IsNotExist(err) {
			t.Errorf("dry run created %s", file.Path)
		}
	}
}

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readObject(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func assertHookCount(t *testing.T, document map[string]any, event string, want int) {
	t.Helper()
	hooks := document["hooks"].(map[string]any)
	entries := hooks[event].([]any)
	if len(entries) != want {
		t.Fatalf("%s hook count = %d, want %d", event, len(entries), want)
	}
}

func assertMCPServers(t *testing.T, document map[string]any) {
	t.Helper()
	servers := document["mcpServers"].(map[string]any)
	if _, ok := servers["existing"]; !ok {
		t.Fatal("existing MCP server was removed")
	}
	if _, ok := servers["trailwire"]; !ok {
		t.Fatal("Trailwire MCP server was not configured")
	}
}
