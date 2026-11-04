package main

import (
	"os"
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
