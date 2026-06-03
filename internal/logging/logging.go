// Package logging configures zerolog for both human-readable terminal output
// and JSON output, switchable by flag.
package logging

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
)

// New returns a zerolog logger writing to w with the given level. If jsonOut
// is true the writer receives line-delimited JSON; otherwise it receives the
// human-friendly console format.
func New(w io.Writer, level string, jsonOut bool) (zerolog.Logger, error) {
	lvl, err := zerolog.ParseLevel(level)
	if err != nil {
		return zerolog.Nop(), fmt.Errorf("parse log level %q: %w", level, err)
	}
	zerolog.TimeFieldFormat = time.RFC3339
	var out io.Writer = w
	if !jsonOut {
		out = zerolog.NewConsoleWriter(func(c *zerolog.ConsoleWriter) {
			c.Out = w
			c.TimeFormat = time.Stamp
		})
	}
	return zerolog.New(out).Level(lvl).Hook(sanitizeLibGMHook{}).With().Timestamp().Logger(), nil
}

// Default builds a logger writing to stderr with sensible defaults.
func Default(level string, jsonOut bool) zerolog.Logger {
	l, err := New(os.Stderr, level, jsonOut)
	if err != nil {
		// fall back to info-level human format if level parse fails
		l, _ = New(os.Stderr, "info", jsonOut)
	}
	return l
}

type sanitizeLibGMHook struct{}

func (sanitizeLibGMHook) Run(e *zerolog.Event, level zerolog.Level, message string) {
	if level != zerolog.WarnLevel {
		return
	}
	switch message {
	case "Got unknown event type", "Got unknown message":
		// Upstream libgm attaches base64-encoded protocol payloads to these
		// warnings. gmcli keeps those blobs out of normal diagnostic logs.
		e.Discard()
	}
}
