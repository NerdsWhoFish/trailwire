package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
)

type Info struct {
	ID      string
	Display string
	Root    string
}

func Discover(cwd string) (Info, error) {
	root, err := git(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return Info{}, errors.New("current directory is not inside a Git repository")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return Info{}, fmt.Errorf("resolve repository root: %w", err)
	}

	remote, remoteErr := git(root, "remote", "get-url", "origin")
	if remoteErr == nil {
		if id, display, ok := NormalizeRemote(remote); ok {
			return Info{ID: id, Display: display, Root: root}, nil
		}
	}

	commonDir, commonErr := git(root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if commonErr != nil {
		commonDir = filepath.Join(root, ".git")
	}
	digest := sha256.Sum256([]byte(filepath.Clean(commonDir)))
	return Info{
		ID:      "local:" + hex.EncodeToString(digest[:12]),
		Display: filepath.Base(root),
		Root:    root,
	}, nil
}

func NormalizeRemote(remote string) (id, display string, ok bool) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return "", "", false
	}

	if !strings.Contains(remote, "://") {
		at := strings.LastIndex(remote, "@")
		colon := strings.Index(remote, ":")
		if at >= 0 && colon > at {
			remote = "ssh://" + remote[:colon] + "/" + remote[colon+1:]
		}
	}
	parsed, err := url.Parse(remote)
	if err != nil || parsed.Hostname() == "" {
		return "", "", false
	}
	path := strings.Trim(strings.TrimSuffix(parsed.Path, ".git"), "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return "", "", false
	}
	ownerRepo := strings.Join(parts[len(parts)-2:], "/")
	host := strings.ToLower(parsed.Hostname())
	id = host + "/" + strings.ToLower(ownerRepo)
	return id, ownerRepo, true
}

func git(cwd string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", cwd}, args...)
	output, err := exec.Command("git", commandArgs...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
