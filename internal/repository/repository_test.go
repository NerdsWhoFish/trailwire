package repository

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverDirectory(t *testing.T) {
	root := t.TempDir()
	want, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(want.ID, "directory:") || strings.Contains(want.ID, root) {
		t.Fatalf("directory identity = %#v", want)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{root, alias, filepath.Join(root, ".")} {
		got, err := Discover(cwd)
		if err != nil || got != want {
			t.Fatalf("Discover(%q) = %#v, %v; want %#v", cwd, got, err, want)
		}
	}
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{child, t.TempDir()} {
		got, err := Discover(cwd)
		if err != nil || got.ID == want.ID {
			t.Fatalf("separate directory %q = %#v, %v", cwd, got, err)
		}
	}
	t.Setenv("PATH", t.TempDir())
	if got, err := Discover(root); err != nil || got != want {
		t.Fatalf("without Git = %#v, %v; want %#v", got, err, want)
	}
}

func TestDiscoverRejectsInvalidDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{file, filepath.Join(root, "missing")} {
		if got, err := Discover(cwd); err == nil {
			t.Fatalf("Discover(%q) = %#v without error", cwd, got)
		}
	}
}

func TestDiscoverPreservesGitIdentities(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	run("init")
	run("config", "commit.gpgsign", "false")
	run("config", "tag.gpgsign", "false")
	run("config", "core.hooksPath", t.TempDir())
	run("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "initial")
	local, err := Discover(root)
	if err != nil || !strings.HasPrefix(local.ID, "local:") {
		t.Fatalf("local repository = %#v, %v", local, err)
	}
	worktree := filepath.Join(t.TempDir(), "worktree")
	run("worktree", "add", "--detach", worktree)
	if got, err := Discover(worktree); err != nil || got.ID != local.ID {
		t.Fatalf("worktree = %#v, %v; want %q", got, err, local.ID)
	}
	run("remote", "add", "origin", "git@github.com:Example/Project.git")
	for _, cwd := range []string{root, worktree} {
		if got, err := Discover(cwd); err != nil || got.ID != "github.com/example/project" {
			t.Fatalf("remote repository = %#v, %v", got, err)
		}
	}
}

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
