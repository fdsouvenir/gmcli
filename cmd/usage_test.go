package cmd

import (
	"errors"
	"testing"
)

func TestExitCodeClassifiesUsageAndOperationalErrors(t *testing.T) {
	if got := ExitCode(nil); got != 0 {
		t.Fatalf("nil exit code = %d", got)
	}
	if got := ExitCode(usageErrorf("bad flag")); got != 2 {
		t.Fatalf("usage exit code = %d", got)
	}
	if got := ExitCode(errors.New(`unknown command "wat" for "gmcli"`)); got != 2 {
		t.Fatalf("unknown command exit code = %d", got)
	}
	if got := ExitCode(errors.New("network failed")); got != 1 {
		t.Fatalf("operational exit code = %d", got)
	}
}

func TestRootWrapsUnknownFlagsAsUsageErrors(t *testing.T) {
	root := Root()
	root.SetArgs([]string{"doctor", "--not-a-real-flag"})
	err := root.Execute()
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("unknown flag error=%v exit=%d", err, ExitCode(err))
	}
}
