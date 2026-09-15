package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fdsouvenir/gmcli/internal/browserauth"
	"github.com/fdsouvenir/gmcli/internal/gm"
	"github.com/fdsouvenir/gmcli/internal/output"
	"github.com/fdsouvenir/gmcli/internal/paths"
	"github.com/fdsouvenir/gmcli/internal/store"
)

const maxCookieInputBytes = 1 << 20

var cookieHeaderPattern = regexp.MustCompile(`(?i)(?:cookie\s*:\s*|(?:--cookie|-b)\s+['"])([^'"\r\n]+)`)

type browserSignIn func(context.Context, string) (map[string]string, error)

func authCmd() *cobra.Command {
	return authCmdWithBrowser(browserauth.SignIn)
}

func authCmdWithBrowser(signIn browserSignIn) *cobra.Command {
	var browserPath string
	var browserTimeout time.Duration
	var cookieFile string
	var forceNew bool
	var allowInsecure bool
	c := &cobra.Command{
		Use:   "auth",
		Short: "Pair with Google Messages using a Google Account",
		Long: "Pair gmcli using the Google Account/emoji flow used by Messages for Web. " +
			"Run gmcli auth to open a temporary Chrome, Chromium or Edge window, sign in " +
			"with the account selected on your phone, then tap the matching emoji. " +
			"The browser closes automatically; no cookie file is needed. " +
			"An existing pairing is refreshed when possible.\n\n" +
			"For remote servers or manual input, --cookies-file accepts JSON or a copied " +
			"/web/config cURL request; - reads stdin. See the README's manual sign-in instructions. " +
			"Credentials are saved only in $STORE/session.json (mode 0600) and are never printed.",
		Example: "  gmcli auth\n" +
			"  gmcli auth --new\n" +
			"  gmcli auth --browser /path/to/chrome\n" +
			"  pbpaste | gmcli auth --cookies-file -",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if browserTimeout <= 0 {
				return usageErrorf("--browser-timeout must be greater than zero")
			}
			ctx, cancel := signalContext(cmd.Context())
			defer cancel()

			var cookies map[string]string
			var err error
			if cookieFile != "" {
				var raw []byte
				raw, err = readCookieInput(cookieFile, cmd.InOrStdin(), allowInsecure)
				if err != nil {
					return err
				}
				cookies, err = parseGoogleCookies(raw)
				if err != nil {
					return wrapUsage(err)
				}
			} else {
				fmt.Fprintln(cmd.ErrOrStderr(), "Opening a temporary sign-in browser...")
				fmt.Fprintln(cmd.ErrOrStderr(), "Sign in with the Google Account selected under Google Messages → Device pairing on your phone.")
				fmt.Fprintln(cmd.ErrOrStderr(), "The window will close automatically when sign-in is complete. Ctrl-C cancels.")
				browserCtx, cancelBrowser := context.WithTimeout(ctx, browserTimeout)
				cookies, err = signIn(browserCtx, browserPath)
				cancelBrowser()
				if err != nil {
					return err
				}
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			layout, err := resolveLayout()
			if err != nil {
				return err
			}
			logger := newLogger()

			if err := invalidatePairingHealth(ctx, layout); err != nil {
				return err
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "Validating Google Account session...")
			res, err := gm.AuthenticateGaia(ctx, layout, logger, cookies, forceNew, func(emoji string) {
				fmt.Fprintln(cmd.ErrOrStderr(), "On your phone, open Google Messages and tap this matching emoji:")
				fmt.Fprintln(cmd.ErrOrStderr())
				fmt.Fprintf(cmd.ErrOrStderr(), "    %s\n", emoji)
				fmt.Fprintln(cmd.ErrOrStderr())
				fmt.Fprintln(cmd.ErrOrStderr(), "Waiting for confirmation... (Ctrl-C to cancel)")
			})
			if err != nil {
				return fmt.Errorf("authenticate: %w", err)
			}
			if flags.jsonOut {
				return output.JSON(cmd.OutOrStdout(), res)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Google Messages %s for %s. session=%s\n", res.Mode, res.Account, res.SessionPath)
			return nil
		},
	}
	c.Flags().StringVar(&browserPath, "browser", "", "Chrome, Chromium or Edge executable for browser sign-in (auto-detected by default)")
	c.Flags().DurationVar(&browserTimeout, "browser-timeout", 5*time.Minute, "time allowed to sign in in the browser")
	c.Flags().StringVar(&cookieFile, "cookies-file", "", "cookie JSON or copied cURL file; use - for stdin")
	c.Flags().BoolVar(&forceNew, "new", false, "create a new phone pairing instead of refreshing an existing Gaia session")
	c.Flags().BoolVar(&allowInsecure, "allow-insecure-cookie-file", false, "allow group/world-readable cookie input files")
	c.MarkFlagsMutuallyExclusive("browser", "cookies-file")
	return c
}

func readCookieInput(path string, stdin io.Reader, allowInsecure bool) ([]byte, error) {
	if path == "-" {
		return readLimitedCookieInput(stdin)
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("cookie input file does not exist; run gmcli auth without --cookies-file for browser sign-in")
		}
		return nil, fmt.Errorf("open cookie input: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("cookie input %s is not a regular file", path)
	}
	if info.Size() > maxCookieInputBytes {
		return nil, fmt.Errorf("cookie input exceeds %d bytes", maxCookieInputBytes)
	}
	if !allowInsecure && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("cookie input %s is group/world accessible (%04o); run `chmod 600 %s` or pass --allow-insecure-cookie-file", path, info.Mode().Perm(), path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open cookie input: %w", err)
	}
	defer f.Close()
	return readLimitedCookieInput(f)
}

func readLimitedCookieInput(r io.Reader) ([]byte, error) {
	limited := io.LimitReader(r, maxCookieInputBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read cookie input: %w", err)
	}
	if len(raw) > maxCookieInputBytes {
		return nil, fmt.Errorf("cookie input exceeds %d bytes", maxCookieInputBytes)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, errorsWithoutInput()
	}
	return raw, nil
}

func errorsWithoutInput() error {
	return usageErrorf("cookie input is empty; provide a JSON object or copied /web/config cURL request")
}

func parseGoogleCookies(raw []byte) (map[string]string, error) {
	text := strings.TrimSpace(string(raw))
	parsed := make(map[string]string)
	if strings.HasPrefix(text, "{") {
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("parse cookie JSON: invalid object")
		}
	} else {
		cookieText := text
		if match := cookieHeaderPattern.FindStringSubmatch(text); len(match) == 2 {
			cookieText = match[1]
		}
		cookieText = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(cookieText), "Cookie:"))
		for _, field := range strings.Split(cookieText, ";") {
			name, value, ok := strings.Cut(strings.TrimSpace(field), "=")
			if !ok || name == "" || value == "" {
				continue
			}
			parsed[name] = value
		}
	}

	return browserauth.FilterCookies(parsed)
}

func invalidatePairingHealth(ctx context.Context, layout paths.Layout) error {
	st, err := store.Open(ctx, layout.Database)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.ObserveHealth(ctx, "session_changed")
}
