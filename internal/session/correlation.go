package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

func ToolFingerprint(toolName string, arguments []byte) (string, string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(toolName))
	index := strings.LastIndex(normalized, "trailwire_")
	if index < 0 {
		return "", "", false
	}
	normalized = normalized[index:]
	var value any = map[string]any{}
	if len(arguments) > 0 && string(arguments) != "null" {
		if err := json.Unmarshal(arguments, &value); err != nil {
			return "", "", false
		}
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", "", false
	}
	sum := sha256.Sum256(append([]byte(normalized+"\x00"), canonical...))
	return normalized, hex.EncodeToString(sum[:]), true
}
