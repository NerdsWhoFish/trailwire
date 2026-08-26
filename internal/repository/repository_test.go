package repository

import "testing"

func TestNormalizeRemote(t *testing.T) {
	tests := map[string]string{
		"git@github.com:TheOutdoorProgrammer/trailwire.git":     "github.com/theoutdoorprogrammer/trailwire",
		"https://github.com/TheOutdoorProgrammer/trailwire.git": "github.com/theoutdoorprogrammer/trailwire",
		"ssh://git@gitlab.example/team/platform/trailwire.git":  "gitlab.example/platform/trailwire",
		"https://user@example.com/acme/widget":                  "example.com/acme/widget",
	}
	for remote, want := range tests {
		got, _, ok := NormalizeRemote(remote)
		if !ok || got != want {
			t.Errorf("NormalizeRemote(%q) = %q, %t, want %q, true", remote, got, ok, want)
		}
	}
}

func TestNormalizeRemoteRejectsLocalPath(t *testing.T) {
	if _, _, ok := NormalizeRemote("../widget.git"); ok {
		t.Fatal("local path was accepted as a remote identity")
	}
}
