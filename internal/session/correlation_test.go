package session

import "testing"

func TestToolFingerprintNormalizesHarnessNamesAndJSON(t *testing.T) {
	firstName, firstHash, ok := ToolFingerprint("MCP:trailwire:trailwire_send", []byte(`{"scope":"repo","body":"hello"}`))
	if !ok {
		t.Fatal("Trailwire tool was not recognized")
	}
	secondName, secondHash, ok := ToolFingerprint("trailwire_send", []byte(`{"body":"hello","scope":"repo"}`))
	if !ok {
		t.Fatal("plain Trailwire tool was not recognized")
	}
	if firstName != "trailwire_send" || secondName != firstName || secondHash != firstHash {
		t.Fatalf("fingerprints = %q/%q and %q/%q", firstName, firstHash, secondName, secondHash)
	}
	if _, _, ok := ToolFingerprint("Read", []byte(`{}`)); ok {
		t.Fatal("non-Trailwire tool was recognized")
	}
}
