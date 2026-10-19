package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
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
		"dist/checksums.txt", "dist/gmcli_linux_amd64_v1/gmcli",
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

// Release binaries must report the tag they were built from, which only works
// while the -X flag names the real module path of cmd.Version and the build
// stays cgo-free so one runner can cross-compile every target.
func TestGoReleaserInjectsVersionFromModulePath(t *testing.T) {
	// The test binary already carries the main module path, the same build
	// info cmd/version.go reads, so go.mod does not need to be parsed.
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi.Main.Path == "" {
		t.Fatal("build info does not carry the main module path")
	}
	raw, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	config := string(raw)
	// {{ .Tag }} keeps the leading "v" so `gmcli --version` matches a
	// `go install ...@vX.Y.Z` build; {{ .Version }} would print 1.2.0.
	want := "-X " + bi.Main.Path + "/cmd.Version={{ .Tag }}"
	if !strings.Contains(config, want) {
		t.Errorf(".goreleaser.yaml does not inject the version with %q", want)
	}
	if !regexp.MustCompile(`(?m)^\s*-\s*CGO_ENABLED=0\s*$`).MatchString(config) {
		t.Error(".goreleaser.yaml does not set CGO_ENABLED=0 for the build")
	}
	for _, target := range []string{"linux", "darwin", "windows", "amd64", "arm64"} {
		if !regexp.MustCompile(`(?m)^\s*-\s*` + target + `\s*$`).MatchString(config) {
			t.Errorf(".goreleaser.yaml does not build for %s", target)
		}
	}
	if !regexp.MustCompile(`(?m)^\s*name_template:\s*checksums\.txt\s*$`).MatchString(config) {
		t.Error(".goreleaser.yaml does not write checksums.txt")
	}
}

// The release workflow must only attach archives for version tags, and only
// after the test suite has passed on that tag.
func TestReleaseWorkflowGatesArchivesOnTests(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)
	if !regexp.MustCompile(`(?m)^\s*tags:\s*\n\s*-\s*"v\*"\s*$`).MatchString(workflow) {
		t.Error("release.yml does not trigger on v* tag pushes")
	}
	jobs := strings.Split(workflow, "\n  release:\n")
	if len(jobs) != 2 {
		t.Fatalf("release.yml: expected one release job, found %d", len(jobs)-1)
	}
	testJob, releaseJob := jobs[0], jobs[1]
	if !strings.Contains(testJob, "\n  test:\n") || !strings.Contains(testJob, "go test") {
		t.Error("release.yml: test job does not run go test before the release job")
	}
	if !regexp.MustCompile(`(?m)^\s*needs:\s*test\s*$`).MatchString(releaseJob) {
		t.Error("release.yml: release job does not depend on the test job")
	}
	if !strings.Contains(releaseJob, "uses: goreleaser/goreleaser-action@") {
		t.Error("release.yml: release job does not run goreleaser-action")
	}
	if !regexp.MustCompile(`(?m)^\s*contents:\s*write\s*$`).MatchString(releaseJob) {
		t.Error("release.yml: release job cannot upload assets without contents: write")
	}
	if regexp.MustCompile(`(?m)^\s*contents:\s*write\s*$`).MatchString(testJob) {
		t.Error("release.yml: the test job must not hold contents: write")
	}
}
