package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type usageError struct {
	err error
}

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func usageErrorf(format string, args ...any) error {
	return usageError{err: fmt.Errorf(format, args...)}
}

func wrapUsage(err error) error {
	if err == nil {
		return nil
	}
	return usageError{err: err}
}

func noArgs(cmd *cobra.Command, args []string) error {
	return wrapUsage(cobra.NoArgs(cmd, args))
}

func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		return wrapUsage(cobra.ExactArgs(n)(cmd, args))
	}
}

func minimumNArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		return wrapUsage(cobra.MinimumNArgs(n)(cmd, args))
	}
}

// ExitCode classifies CLI failures for shell and agent callers.
// 0 is success, 1 is an operational failure, and 2 is invalid usage.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var usage usageError
	if errors.As(err, &usage) {
		return 2
	}
	// Cobra constructs unknown-command failures internally, outside the
	// positional and flag validators we can wrap above.
	if strings.HasPrefix(err.Error(), "unknown command ") {
		return 2
	}
	return 1
}
