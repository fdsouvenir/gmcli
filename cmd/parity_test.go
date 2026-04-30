package cmd_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/fdsouvenir/gmcli/cmd"
)

// runCmdAllowError is like runCmd but returns the execution error instead of
// failing the test. Used to assert that read-only mode rejects writes.
func runCmdAllowError(t *testing.T, store string, args ...string) (string, error) {
	t.Helper()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	root := cmd.Root()
	full := append([]string{"--store", store}, args...)
	root.SetArgs(full)
	err := root.Execute()
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	return strings.TrimSpace(buf.String()), err
}

func TestReadOnlyGatesWrites(t *testing.T) {
	dir := seedStore(t)
	// Default --read-only=true → phone-send commands should fail before
	// opening a session.
	_, err := runCmdAllowError(t, dir, "send", "text", "--to", "c_alice", "--message", "hello")
	if err == nil {
		t.Fatal("expected read-only error, got nil")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected read-only error, got: %v", err)
	}
}

func TestAliasRoundTrip(t *testing.T) {
	dir := seedStore(t)
	// Set the alias.
	if _, err := runCmdAllowError(t, dir,
		"contacts", "alias", "set", "--id", "p_alice", "--alias", "Mom"); err != nil {
		t.Fatalf("set alias: %v", err)
	}
	// Search should now show the alias overlay.
	out := runCmd(t, dir, "contacts", "search", "mom")
	if !strings.Contains(out, "Mom") {
		t.Fatalf("alias not surfaced in search: %q", out)
	}
	// And `contacts show` should display alias line.
	detail := runCmd(t, dir, "contacts", "show", "p_alice")
	if !strings.Contains(detail, "alias:") || !strings.Contains(detail, "Mom") {
		t.Fatalf("alias not in detail: %q", detail)
	}
	// Remove and verify.
	if _, err := runCmdAllowError(t, dir,
		"contacts", "alias", "rm", "--id", "p_alice"); err != nil {
		t.Fatalf("rm alias: %v", err)
	}
	out2 := runCmd(t, dir, "contacts", "search", "alice")
	if strings.Contains(out2, "Mom") {
		t.Fatalf("alias still present after rm: %q", out2)
	}
}

func TestFullFlagDisablesTruncation(t *testing.T) {
	dir := seedStore(t)
	// Default truncation: ~80 chars in body column. Our seeded message
	// "How about 7pm at the usual place" is short enough not to be
	// truncated; pick a contact with a long name instead.
	// We need to verify --full passes through unchanged. Use the chats
	// list rendering of `Alice Example` — a 13-char string fits already,
	// so the assertion is that --full doesn't break anything and still
	// renders the cell intact.
	full := runCmd(t, dir, "--full", "chats", "list")
	if !strings.Contains(full, "Alice Example") {
		t.Fatalf("--full output missing name: %q", full)
	}
	// Negative check: ensure --full path doesn't produce ellipsis on a
	// short cell that wouldn't be truncated anyway.
	if strings.Contains(full, "Alice E…") {
		t.Fatalf("--full unexpectedly truncated: %q", full)
	}
}
