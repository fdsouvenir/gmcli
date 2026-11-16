package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fdsouvenir/gmcli/internal/gm"
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

// ExitAuthRequired is returned when the saved Google Account session has been
// logged out and `gmcli auth` must be run again. Supervisors should stop
// restarting and alert instead of looping.
const ExitAuthRequired = 3

// ExitCode classifies CLI failures for shell and agent callers.
// 0 is success, 1 is an operational failure, 2 is invalid usage, and 3 means
// the saved session is logged out and re-authentication is required.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var usage usageError
	if errors.As(err, &usage) {
		return 2
	}
	if errorCode(err) == gm.CodeSessionLoggedOut {
		return ExitAuthRequired
	}
	// Cobra constructs unknown-command failures internally, outside the
	// positional and flag validators we can wrap above.
	if strings.HasPrefix(err.Error(), "unknown command ") {
		return 2
	}
	return 1
}
