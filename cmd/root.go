// Package cmd implements the gmcli command tree.
package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/fdsouvenir/gmcli/internal/logging"
	"github.com/fdsouvenir/gmcli/internal/output"
	"github.com/fdsouvenir/gmcli/internal/paths"
	"github.com/fdsouvenir/gmcli/internal/store"
)

// globalFlags is populated by persistent flags on the root command and
// consumed by subcommands via context or direct reference.
type globalFlags struct {
	storeDir string
	logLevel string
	jsonOut  bool
	readOnly bool
	full     bool
}

var flags globalFlags

// requireWritable returns a non-nil error when the user has not opted into
// phone-send commands (--read-only=false). Local store maintenance remains
// allowed in read-only mode so sync, backfill, aliases, and media downloads
// keep working like wacli's local archive operations.
func requireWritable() error {
	if flags.readOnly {
		return errReadOnly
	}
	return nil
}

// errReadOnly is the canonical error surfaced by write subcommands when
// --read-only is in effect (the default).
var errReadOnly = errReadOnlyT{}

type errReadOnlyT struct{}

func (errReadOnlyT) Error() string {
	return "read-only mode: this command sends through the phone. Re-run with --read-only=false to allow it."
}

// truncate caps a string to n runes when --full is unset; otherwise returns
// it verbatim. Use this everywhere render code wants to truncate.
func truncate(s string, n int) string {
	if flags.full {
		return s
	}
	return output.Truncate(s, n)
}

// Root constructs the top-level *cobra.Command.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "gmcli",
		Short:         "Local-first Google Messages CLI and archive",
		Long:          "gmcli pairs with Google Messages, archives conversations into SQLite, and exposes them through a CLI.\nLicensed under AGPL-3.0; see `gmcli version` for attribution details.",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.PersistentFlags().StringVar(&flags.storeDir, "store", "", "data directory (default: $XDG_STATE_HOME/gmcli or ~/.local/state/gmcli)")
	root.PersistentFlags().StringVar(&flags.logLevel, "log-level", "info", "log verbosity: trace, debug, info, warn, error")
	root.PersistentFlags().BoolVar(&flags.jsonOut, "json", false, "emit machine-readable JSON output where applicable")
	root.PersistentFlags().BoolVar(&flags.readOnly, "read-only", true, "block commands that send texts or other phone mutations")
	root.PersistentFlags().BoolVar(&flags.full, "full", false, "disable truncation in tabular output")

	root.AddCommand(authCmd())
	root.AddCommand(syncCmd())
	root.AddCommand(versionCmd())
	root.AddCommand(doctorCmd())
	root.AddCommand(messagesCmd())
	root.AddCommand(contactsCmd())
	root.AddCommand(chatsCmd())
	root.AddCommand(historyCmd())
	root.AddCommand(sendCmd())
	root.AddCommand(mediaCmd())
	return root
}

// openStore resolves the layout and opens the SQLite store for query
// commands. Caller must Close.
func openStore() (*store.Store, error) {
	layout, err := resolveLayout()
	if err != nil {
		return nil, err
	}
	return store.Open(context.Background(), layout.Database)
}

// resolveLayout applies --store and ensures the directory tree exists.
func resolveLayout() (paths.Layout, error) {
	layout, err := paths.Resolve(flags.storeDir)
	if err != nil {
		return paths.Layout{}, err
	}
	if err := layout.EnsureDirs(); err != nil {
		return paths.Layout{}, err
	}
	return layout, nil
}

func newLogger() zerolog.Logger {
	return logging.Default(flags.logLevel, flags.jsonOut)
}

// signalContext returns a context cancelled on SIGINT/SIGTERM. Callers must
// invoke the returned cancel to release the signal handler.
func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigs:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigs)
	}()
	return ctx, cancel
}
