package gm

import (
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/mautrix-gmessages/pkg/libgm"
)

func TestSaveAuthAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if err := os.WriteFile(path, []byte("previous-session"), 0600); err != nil {
		t.Fatal(err)
	}
	// A predictable old temporary path must not be followed or truncated.
	sentinel := filepath.Join(dir, "sentinel")
	if err := os.WriteFile(sentinel, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, path+".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := saveAuth(path, libgm.NewAuthData()); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAuth(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %o", info.Mode().Perm())
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "untouched" {
		t.Fatalf("sentinel changed: %v", err)
	}
	temps, err := filepath.Glob(filepath.Join(dir, ".session-*.tmp"))
	if err != nil || len(temps) != 0 {
		t.Fatalf("temporary files remain: %v %v", temps, err)
	}
}

func TestSaveAuthRenameFailurePreservesExistingTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	prior := filepath.Join(path, "prior")
	if err := os.WriteFile(prior, []byte("previous-session"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveAuth(path, libgm.NewAuthData()); err == nil {
		t.Fatal("expected rename failure")
	}
	data, err := os.ReadFile(prior)
	if err != nil || string(data) != "previous-session" {
		t.Fatalf("prior state changed: %v", err)
	}
	temps, err := filepath.Glob(filepath.Join(dir, ".session-*.tmp"))
	if err != nil || len(temps) != 0 {
		t.Fatalf("temporary files remain: %v %v", temps, err)
	}
}
