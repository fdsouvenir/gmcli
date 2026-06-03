package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/mdp/qrterminal/v3"
	"github.com/spf13/cobra"
	"rsc.io/qr"

	"github.com/fdsouvenir/gmcli/internal/gm"
)

func authCmd() *cobra.Command {
	var qrPNG string
	c := &cobra.Command{
		Use:   "auth",
		Short: "Pair with Google Messages by scanning a QR code",
		Long: "Run once to pair gmcli with your phone. The command renders a QR code; " +
			"open Google Messages on your phone, go to Settings -> Device pairing -> QR code " +
			"scanner, and scan the code. The session is saved to $STORE/session.json (mode 0600).",
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, err := resolveLayout()
			if err != nil {
				return err
			}
			logger := newLogger()
			ctx, cancel := signalContext(context.Background())
			defer cancel()

			fmt.Fprintln(os.Stderr, "Requesting pairing token from Google...")
			res, err := gm.Pair(ctx, layout, logger, func(qrURL string) {
				if qrPNG != "" {
					if err := writeQRPNG(qrPNG, qrURL); err != nil {
						fmt.Fprintf(os.Stderr, "Failed to write QR PNG: %v\n", err)
					} else {
						fmt.Fprintf(os.Stderr, "Wrote QR PNG: %s\n", qrPNG)
					}
				}
				fmt.Fprintln(os.Stderr, "Scan this QR code from Google Messages -> Device pairing:")
				fmt.Fprintln(os.Stderr)
				qrterminal.GenerateHalfBlock(qrURL, qrterminal.L, os.Stderr)
				fmt.Fprintln(os.Stderr)
				fmt.Fprintln(os.Stderr, "Or paste this URL into a QR generator:")
				fmt.Fprintln(os.Stderr, "  ", qrURL)
				fmt.Fprintln(os.Stderr)
				fmt.Fprintln(os.Stderr, "Waiting for pairing... (Ctrl-C to cancel)")
			})
			if err != nil {
				return fmt.Errorf("pair: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Paired. phone_id=%s session=%s\n", res.PhoneID, res.SessionPath)
			return nil
		},
	}
	c.Flags().StringVar(&qrPNG, "qr-png", "", "write pairing QR code to a PNG file")
	return c
}

func writeQRPNG(path, text string) error {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return fmt.Errorf("encode QR: %w", err)
	}
	if err := os.WriteFile(path, code.PNG(), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
