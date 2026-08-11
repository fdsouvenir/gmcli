package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fdsouvenir/gmcli/internal/gm"
	"github.com/fdsouvenir/gmcli/internal/output"
)

const maxCookieInputBytes = 1 << 20

var (
	requiredGoogleCookies = []string{"SID", "HSID", "SSID", "OSID", "APISID", "SAPISID"}
	allowedGoogleCookies  = map[string]struct{}{
		"SID": {}, "HSID": {}, "SSID": {}, "OSID": {}, "APISID": {}, "SAPISID": {},
		"__Secure-1PSIDTS": {},
	}
	cookieHeaderPattern = regexp.MustCompile(`(?i)(?:cookie\s*:\s*|(?:--cookie|-b)\s+['"])([^'"\r\n]+)`)
)

func authCmd() *cobra.Command {
	var cookieFile string
	var forceNew bool
	var allowInsecure bool
	c := &cobra.Command{
		Use:   "auth",
		Short: "Pair with Google Messages using a Google Account",
		Long: "Pair gmcli using the Google Account/emoji flow used by Messages for Web. " +
			"Export the /web/config request from a private browser window as cURL, or provide " +
			"a JSON object containing the required Google cookies. Cookie values are persisted " +
			"inside $STORE/session.json (mode 0600) and are never printed.",
		Example: "  gmcli auth --cookies-file ~/private/gmessages-cookies.json\n" +
			"  pbpaste | gmcli auth --cookies-file -\n" +
			"  gmcli auth --cookies-file cookies.txt --new",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cookieFile == "" {
				return usageErrorf("--cookies-file is required; use - to read JSON or copied cURL from stdin")
			}
			raw, err := readCookieInput(cookieFile, cmd.InOrStdin(), allowInsecure)
			if err != nil {
				return err
			}
			cookies, err := parseGoogleCookies(raw)
			if err != nil {
				return wrapUsage(err)
			}

			layout, err := resolveLayout()
			if err != nil {
				return err
			}
			logger := newLogger()
			ctx, cancel := signalContext(context.Background())
			defer cancel()

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
	c.Flags().StringVar(&cookieFile, "cookies-file", "", "cookie JSON or copied cURL file; use - for stdin")
	c.Flags().BoolVar(&forceNew, "new", false, "create a new phone pairing instead of refreshing an existing Gaia session")
	c.Flags().BoolVar(&allowInsecure, "allow-insecure-cookie-file", false, "allow group/world-readable cookie input files")
	return c
}

func readCookieInput(path string, stdin io.Reader, allowInsecure bool) ([]byte, error) {
	if path == "-" {
		return readLimitedCookieInput(stdin)
	}
	info, err := os.Stat(path)
	if err != nil {
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

	filtered := make(map[string]string, len(allowedGoogleCookies))
	for name, value := range parsed {
		if _, ok := allowedGoogleCookies[name]; ok && strings.TrimSpace(value) != "" {
			filtered[name] = value
		}
	}
	var missing []string
	for _, name := range requiredGoogleCookies {
		if filtered[name] == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("cookie input is missing required names: %s", strings.Join(missing, ", "))
	}
	return filtered, nil
}
