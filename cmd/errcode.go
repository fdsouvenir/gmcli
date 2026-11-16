package cmd

import (
	"context"
	"errors"
	"io"
)

// Generic error codes used by command event streams when a failure carries no
// more specific code of its own.
const (
	codeCancelled = "cancelled" // interrupted by Ctrl-C or SIGTERM
	codeTimeout   = "timeout"   // a deadline passed
	codeUsage     = "usage"     // invalid flags or arguments
	codeFailed    = "failed"    // any other failure
)

// coded is implemented by errors that carry a stable machine-readable code.
type coded interface {
	Code() string
}

// commandError is the final JSON line a long-running command writes to stdout
// under --json when it stops with an error, so supervisors can read the code
// without parsing stderr logs.
type commandError struct {
	Event   string `json:"event"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// reportCommandError emits the error line under --json and returns err
// unchanged for the normal exit path.
func reportCommandError(w io.Writer, jsonOut bool, err error) error {
	if err == nil || !jsonOut {
		return err
	}
	_ = writeJSONLine(w, commandError{Event: "error", Code: errorCode(err), Message: err.Error()})
	return err
}

// errorCode returns the stable code for err: the innermost coded error when
// one exists, otherwise a generic classification.
func errorCode(err error) string {
	if err == nil {
		return ""
	}
	var c coded
	if errors.As(err, &c) {
		return c.Code()
	}
	var usage usageError
	switch {
	case errors.As(err, &usage):
		return codeUsage
	case errors.Is(err, context.Canceled):
		return codeCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return codeTimeout
	default:
		return codeFailed
	}
}
