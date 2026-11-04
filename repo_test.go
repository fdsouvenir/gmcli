package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Third-party actions must stay pinned to full commit SHAs so a moved tag
// cannot change what CI runs.
func TestWorkflowActionsPinnedToSHA(t *testing.T) {
	// GitHub runs both .yml and .yaml workflow files.
	var files []string
	for _, pattern := range []string{"*.yml", "*.yaml"} {
		matches, err := filepath.Glob(filepath.Join(".github", "workflows", pattern))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		t.Fatal("no workflow files found")
	}
	uses := regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*([^\s#]+)`)
	// A commit SHA for repository actions, an image digest for docker:// refs.
	pinned := regexp.MustCompile(`@([0-9a-f]{40}|sha256:[0-9a-f]{64})$`)
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range uses.FindAllStringSubmatch(string(raw), -1) {
			// YAML allows the value to be quoted; a trailing quote would
			// otherwise defeat the end-anchored SHA match.
			ref := strings.Trim(m[1], `"'`)
			if ref == "" || strings.HasPrefix(ref, ".") {
				continue
			}
			if !pinned.MatchString(ref) {
				t.Errorf("%s: %s is not pinned to a commit SHA", file, ref)
			}
		}
	}
}

// Credential exports and local artifacts must never be committable.
func TestGitignoreCoversSecrets(t *testing.T) {
	if err := exec.Command("git", "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		t.Skip("not a git checkout")
	}
	// git check-ignore exits 0 for ignored, 1 for not ignored, and 128 on
	// error; only the first two are answers. The developer's global excludes
	// file is disabled so the verdict comes from the repository's .gitignore
	// alone, not from a ~/.config/git/ignore entry that happens to overlap.
	ignored := func(path string) bool {
		err := exec.Command("git", "-c", "core.excludesFile=/dev/null", "check-ignore", "-q", "--no-index", path).Run()
		if err == nil {
			return true
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false
		}
		t.Fatalf("git check-ignore %s: %v", path, err)
		return false
	}
	for _, path := range []string{
		"session.json", "gmcli.db", "cookies.txt", "cookies.json", "auth.cookies",
		".env", ".env.local", "server.key", "cert.pem", "coverage.out",
		".claude/settings.local.json", ".claude/worktrees/feature/main.go",
	} {
		if !ignored(path) {
			t.Errorf("%s is not ignored by .gitignore", path)
		}
	}
	// Shared agent configuration, direnv config, and the env template must remain committable.
	for _, path := range []string{".env.example", ".envrc", ".claude/settings.json", ".claude/skills/README.md"} {
		if ignored(path) {
			t.Errorf("%s is ignored by .gitignore but should be committable", path)
		}
	}
}
