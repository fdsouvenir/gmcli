package skills_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeCompatibilityPreflight(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	script, err := filepath.Abs("google-messages/scripts/check-runtime.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, openclaw, gmcli, wantError string
		failVersion                      bool
	}{
		{name: "minimum", openclaw: "OpenClaw 2026.8.1", gmcli: "gmcli v1.1.0"},
		{name: "newer", openclaw: "2026.10.1", gmcli: "gmcli v1.1.0"},
		{name: "build metadata", openclaw: "v2026.8.1+build.7", gmcli: "gmcli v1.1.0"},
		{name: "old openclaw", openclaw: "2026.7.9", gmcli: "gmcli v1.1.0", wantError: "openclaw 2026.8.1 or newer"},
		{name: "old gmcli", openclaw: "2026.8.1", gmcli: "gmcli v1.0.0", wantError: "gmcli 1.1.0 or newer"},
		{name: "prerelease", openclaw: "2026.8.1-beta.3", gmcli: "gmcli v1.1.0", wantError: "openclaw 2026.8.1 or newer"},
		{name: "unversioned", openclaw: "2026.8.1", gmcli: "gmcli dev", wantError: "Could not identify a released gmcli version"},
		{name: "missing runtime", gmcli: "gmcli v1.1.0", wantError: "openclaw is required"},
		{name: "failed version command", openclaw: "2026.8.1", gmcli: "gmcli v1.1.0", failVersion: true, wantError: "Could not read openclaw version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, version := range map[string]string{"openclaw": tc.openclaw, "gmcli": tc.gmcli} {
				if version == "" {
					continue
				}
				flag := "--version"
				if name == "gmcli" {
					flag = "version"
				}
				stub := "#!/bin/sh\n[ \"$#\" -eq 1 ] && [ \"$1\" = '" + flag + "' ] || exit 99\nprintf '%s\\n' '" + version + "'\n"
				if tc.failVersion {
					stub += "exit 1\n"
				}
				if err := os.WriteFile(filepath.Join(dir, name), []byte(stub), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(bash, script)
			cmd.Env = []string{"PATH=" + dir}
			out, err := cmd.CombinedOutput()
			if tc.wantError == "" {
				if err != nil || !strings.Contains(string(out), "Runtime compatible") {
					t.Fatalf("compatible runtime rejected: %v, %s", err, out)
				}
			} else if err == nil || !strings.Contains(string(out), tc.wantError) {
				t.Fatalf("want error containing %q; got %v, %s", tc.wantError, err, out)
			}
		})
	}
}
