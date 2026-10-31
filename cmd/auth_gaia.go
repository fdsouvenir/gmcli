package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/fdsouvenir/gmcli/internal/gm"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func gaiaTerminalGuard(human, json bool, inputTTY, outputTTY, errorTTY bool) error {
	if !human || json || !inputTTY || !outputTTY || !errorTTY {
		return fmt.Errorf("Gaia pairing requires --human-terminal in a private, unrecorded human-controlled terminal; no JSON, pipes, agents, or shared terminal sessions")
	}
	return nil
}

func authGaiaCmd() *cobra.Command {
	var human bool
	c := &cobra.Command{
		Use: "gaia", Short: "Pair a Google account in a private human-controlled terminal",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := gaiaTerminalGuard(human, flags.jsonOut, term.IsTerminal(int(os.Stdin.Fd())), term.IsTerminal(int(os.Stdout.Fd())), term.IsTerminal(int(os.Stderr.Fd()))); err != nil {
				return err
			}
			// Never accept credentials in arguments, environment variables or logging.
			tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
			if err != nil {
				return fmt.Errorf("private controlling terminal unavailable")
			}
			defer tty.Close()
			fmt.Fprintln(tty, "Use your own signed-in messages.google.com browser. Paste its Cookie header below (hidden input). Never share it with an agent or transcript.")
			raw, err := term.ReadPassword(int(tty.Fd()))
			fmt.Fprintln(tty)
			if err != nil {
				return fmt.Errorf("could not read hidden cookie input")
			}
			cookies, err := gm.ParseGaiaCookies(string(raw))
			clear(raw)
			if err != nil {
				return err
			}
			layout, err := resolveLayout()
			if err != nil {
				return err
			}
			ctx, cancel := gaiaContext()
			defer cancel()
			if err := invalidatePairingHealth(ctx, layout); err != nil {
				return err
			}
			_, err = gm.PairGaia(ctx, layout, cookies, func(emoji string) { fmt.Fprintf(tty, "Select this matching emoji on your phone: %s\n", emoji) })
			if err != nil {
				return err
			}
			fmt.Fprintln(tty, "Google-account session saved. Pairing alone does not verify sync health.")
			return nil
		},
	}
	c.Flags().BoolVar(&human, "human-terminal", false, "confirm this is a private, unrecorded terminal controlled by you, not an agent")
	return c
}

// Unlike signalContext, this deliberately leaves SIGINT/SIGTERM at OS defaults.
// Human cancellation must terminate even if upstream ignores context cancellation.
func gaiaContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), gm.PairTimeout)
}
