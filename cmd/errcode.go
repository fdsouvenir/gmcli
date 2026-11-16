package cmd

import (
	"context"
	"errors"
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
