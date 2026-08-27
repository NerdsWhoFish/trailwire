package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/theoutdoorprogrammer/trailwire/internal/config"
)

type Options struct {
	HomeDir    string
	BinaryPath string
	ConfigPath string
	DryRun     bool
}

type FileResult struct {
	Path    string
	Changed bool
}

type Result struct {
	Files []FileResult
}

func Install(options Options) (Result, error) {
	home := options.HomeDir
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return Result{}, fmt.Errorf("find home directory: %w", err)
		}
	}
	binary := options.BinaryPath
	if binary == "" {
		var err error
		binary, err = findBinary()
		if err != nil {
			return Result{}, err
		}
	}
	configPath := options.ConfigPath
	if configPath == "" {
		var err error
		configPath, err = config.DefaultPath()
		if err != nil {
			return Result{}, err
		}
	}

	result := Result{}
	cfg, err := config.Load(configPath)
	if err != nil {
		return result, err
	}
	configChanged := cfg.NeedsSave()
	for _, harness := range []string{"claude", "codex", "cursor"} {
		if _, created, err := cfg.EnsureAgent(harness, ""); err != nil {
			return result, err
		} else if created {
			configChanged = true
		}
	}
	if configChanged && !options.DryRun {
		if err := config.Save(configPath, cfg); err != nil {
			return result, err
		}
	}
	result.Files = append(result.Files, FileResult{Path: configPath, Changed: configChanged})

	files := []struct {
		path   string
		update func([]byte) ([]byte, error)
	}{
		{filepath.Join(home, ".claude", "settings.json"), func(data []byte) ([]byte, error) {
			return updateHookJSON(data, "claude", binary)
		}},
		{filepath.Join(home, ".claude.json"), func(data []byte) ([]byte, error) {
			return updateMCPJSON(data, "claude", binary)
		}},
		{filepath.Join(home, ".codex", "hooks.json"), func(data []byte) ([]byte, error) {
			return updateHookJSON(data, "codex", binary)
		}},
		{filepath.Join(home, ".codex", "config.toml"), func(data []byte) ([]byte, error) {
			return updateCodexTOML(data, binary), nil
		}},
		{filepath.Join(home, ".cursor", "hooks.json"), func(data []byte) ([]byte, error) {
			return updateHookJSON(data, "cursor", binary)
		}},
		{filepath.Join(home, ".cursor", "mcp.json"), func(data []byte) ([]byte, error) {
			return updateMCPJSON(data, "cursor", binary)
		}},
	}
	for _, file := range files {
		changed, err := updateFile(file.path, file.update, options.DryRun)
		if err != nil {
			return result, fmt.Errorf("configure %s: %w", file.path, err)
		}
		result.Files = append(result.Files, FileResult{Path: file.path, Changed: changed})
	}
	return result, nil
}

func findBinary() (string, error) {
	if path, err := exec.LookPath("trailwire"); err == nil {
		absolute, err := filepath.Abs(path)
		if err == nil {
			return absolute, nil
		}
	}
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find trailwire executable: %w", err)
	}
	return filepath.Abs(path)
}

func updateHookJSON(data []byte, harness, binary string) ([]byte, error) {
	document, err := decodeJSONObject(data)
	if err != nil {
		return nil, err
	}
	hooks := objectField(document, "hooks")
	command := shellCommand(binary, "hook", harness)
	if harness == "cursor" {
		for _, event := range []string{"sessionStart", "beforeSubmitPrompt", "preToolUse", "postToolUse", "postToolUseFailure", "stop", "sessionEnd"} {
			entry := map[string]any{"command": command}
			hooks[event] = replaceOwned(hooks[event], harness, entry, false)
		}
		document["version"] = 1
	} else {
		events := []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "Stop", "SessionEnd"}
		if harness == "codex" {
			events = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "SessionEnd"}
		}
		for _, event := range events {
			group := map[string]any{
				"hooks": []any{map[string]any{
					"type": "command", "command": command, "timeout": 10,
				}},
			}
			if event == "PreToolUse" || event == "PostToolUse" || event == "PostToolUseFailure" || event == "SessionStart" {
				group["matcher"] = "*"
			}
			hooks[event] = replaceOwned(hooks[event], harness, group, true)
		}
	}
	document["hooks"] = hooks
	return encodeJSONObject(document)
}

func updateMCPJSON(data []byte, harness, binary string) ([]byte, error) {
	document, err := decodeJSONObject(data)
	if err != nil {
		return nil, err
	}
	servers := objectField(document, "mcpServers")
	servers["trailwire"] = map[string]any{
		"type": "stdio", "command": binary, "args": []string{"--harness", harness, "mcp"},
	}
	document["mcpServers"] = servers
	return encodeJSONObject(document)
}

func updateCodexTOML(data []byte, binary string) []byte {
	const startMarker = "# trailwire:start"
	const endMarker = "# trailwire:end"
	block := startMarker + "\n[mcp_servers.trailwire]\ncommand = " + strconv.Quote(binary) + "\nargs = [\"--harness\", \"codex\", \"mcp\"]\n" + endMarker
	text := string(data)
	if start := strings.Index(text, startMarker); start >= 0 {
		if end := strings.Index(text[start:], endMarker); end >= 0 {
			end += start + len(endMarker)
			return []byte(text[:start] + block + text[end:])
		}
	}
	section := regexp.MustCompile(`(?m)^\[mcp_servers(?:\.trailwire|\."trailwire")\][ \t]*$`)
	if location := section.FindStringIndex(text); location != nil {
		end := len(text)
		if next := regexp.MustCompile(`(?m)^\[[^\r\n]+\][ \t]*$`).FindStringIndex(text[location[1]:]); next != nil {
			end = location[1] + next[0]
		}
		return []byte(text[:location[0]] + block + "\n" + text[end:])
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if text != "" {
		text += "\n"
	}
	return []byte(text + block + "\n")
}

func replaceOwned(existing any, harness string, replacement map[string]any, grouped bool) []any {
	items, _ := existing.([]any)
	kept := make([]any, 0, len(items)+1)
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok || !ownedHook(entry, harness, grouped) {
			kept = append(kept, item)
		}
	}
	return append(kept, replacement)
}

func ownedHook(entry map[string]any, harness string, grouped bool) bool {
	if !grouped {
		command, _ := entry["command"].(string)
		return strings.Contains(command, "trailwire") && strings.Contains(command, "hook "+harness)
	}
	hooks, _ := entry["hooks"].([]any)
	for _, raw := range hooks {
		hook, _ := raw.(map[string]any)
		command, _ := hook["command"].(string)
		if strings.Contains(command, "trailwire") && strings.Contains(command, "hook "+harness) {
			return true
		}
	}
	return false
}

func shellCommand(binary string, args ...string) string {
	parts := []string{shellQuote(binary)}
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	if runtime.GOOS == "windows" {
		return strconv.Quote(value)
	}
	if value != "" && !strings.ContainsAny(value, " \t\n'\"\\$`!&|;()<>*?[]{}") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func decodeJSONObject(data []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	if document == nil {
		document = map[string]any{}
	}
	return document, nil
}

func objectField(document map[string]any, key string) map[string]any {
	if object, ok := document[key].(map[string]any); ok {
		return object
	}
	return map[string]any{}
}

func encodeJSONObject(document map[string]any) ([]byte, error) {
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode JSON: %w", err)
	}
	return append(data, '\n'), nil
}

func updateFile(path string, update func([]byte) ([]byte, error), dryRun bool) (bool, error) {
	original, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	updated, err := update(original)
	if err != nil {
		return false, err
	}
	if bytes.Equal(original, updated) {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".trailwire-*")
	if err != nil {
		return false, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return false, err
	}
	if _, err := temporary.Write(updated); err != nil {
		temporary.Close()
		return false, err
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return false, err
	}
	return true, nil
}
