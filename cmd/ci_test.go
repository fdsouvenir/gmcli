package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Unit tests for .github/workflows/ci.yml configuration and compliance
func TestCIWorkflowStructure(t *testing.T) {
	path := filepath.Join("..", ".github", "workflows", "ci.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read %s: %v", path, err)
	}
	content := string(raw)

	// 1. Top-level permissions: {}
	if !strings.Contains(content, "permissions: {}") {
		t.Error("ci.yml must specify top-level permissions: {} for least-privilege token access")
	}

	// 2. Concurrency block
	if !strings.Contains(content, "concurrency:") || !strings.Contains(content, "cancel-in-progress: true") {
		t.Error("ci.yml must define concurrency group with cancel-in-progress: true")
	}

	// 3. Timeout on jobs
	if !strings.Contains(content, "timeout-minutes:") {
		t.Error("ci.yml jobs must declare timeout-minutes")
	}

	// 4. Action SHA-pinning (no mutable @vX tags for actions/*)
	actionRegex := regexp.MustCompile(`uses:\s*(actions/[a-zA-Z0-9_-]+)@([^\s#]+)`)
	matches := actionRegex.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		t.Fatal("no action references found in ci.yml")
	}
	sha40 := regexp.MustCompile(`^[0-9a-f]{40}$`)
	for _, match := range matches {
		actionName := match[1]
		ref := match[2]
		if !sha40.MatchString(ref) {
			t.Errorf("action %s pinned to %q; must be pinned to 40-character commit SHA", actionName, ref)
		}
	}

	// 5. Checkout persist-credentials: false
	if !strings.Contains(content, "persist-credentials: false") {
		t.Error("actions/checkout step must set persist-credentials: false")
	}
}

func TestCIWorkflowSteps(t *testing.T) {
	path := filepath.Join("..", ".github", "workflows", "ci.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read %s: %v", path, err)
	}
	content := string(raw)

	// 1. gofmt formatting check
	if !strings.Contains(content, "gofmt") {
		t.Error("ci.yml must include gofmt check")
	}

	// 2. vet step
	if !strings.Contains(content, "go vet ./...") {
		t.Error("ci.yml must include go vet check")
	}

	// 3. race test
	if !strings.Contains(content, "-race") {
		t.Error("ci.yml must include race detector in test execution")
	}

	// 4. govulncheck dependency vulnerability audit
	if !strings.Contains(content, "govulncheck") {
		t.Error("ci.yml must include govulncheck vulnerability audit")
	}
	if !strings.Contains(content, "govulncheck@v1.9.0") {
		t.Error("ci.yml must pin govulncheck to v1.9.0 for Go 1.25+ compatibility")
	}

	// 5. shellcheck for scripts
	if !strings.Contains(content, "shellcheck") {
		t.Error("ci.yml must include shellcheck for shell scripts")
	}
}

// Functional tests ensuring the gates enforced by CI actually pass on the codebase
func TestRepositoryFormattingFunctional(t *testing.T) {
	cmd := exec.Command("gofmt", "-l", "..")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("gofmt failed: %v", err)
	}
	unformatted := strings.TrimSpace(string(out))
	if unformatted != "" {
		t.Errorf("unformatted files detected:\n%s", unformatted)
	}
}

// Negative functional test: verify that gofmt gate detects formatting violations
func TestFormattingDetectionNegativeFunctional(t *testing.T) {
	dir := t.TempDir()
	badFile := filepath.Join(dir, "bad.go")
	// Deliberately poorly indented code
	badCode := "package main\n\nfunc main() {\nvar x=1\n_ = x\n}\n"
	if err := os.WriteFile(badFile, []byte(badCode), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	cmd := exec.Command("gofmt", "-l", badFile)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("gofmt failed: %v", err)
	}
	if !strings.Contains(string(out), badFile) {
		t.Errorf("expected gofmt to flag %s, got output: %q", badFile, string(out))
	}
}

func TestShellScriptLintingFunctional(t *testing.T) {
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip("shellcheck not found in PATH; skipping functional lint test")
	}

	scriptPath := filepath.Join("..", "skills", "google-messages", "scripts", "check-runtime.sh")
	cmd := exec.Command("shellcheck", scriptPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("shellcheck failed for %s:\n%s", scriptPath, string(out))
	}
}

func TestBuildExecutableFunctional(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "gmcli-test-bin")
	cmd := exec.Command("go", "build", "-o", bin, "..")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v\nOutput: %s", err, string(out))
	}
	if fi, err := os.Stat(bin); err != nil || fi.Size() == 0 {
		t.Fatalf("expected binary to be created and non-empty, err: %v", err)
	}
}
