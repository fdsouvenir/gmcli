package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/fdsouvenir/gmcli/internal/browserauth"
	"github.com/fdsouvenir/gmcli/internal/gm"
	"github.com/fdsouvenir/gmcli/internal/paths"
	"github.com/fdsouvenir/gmcli/internal/store"
)

const completeCookieJSON = `{
  "SID":"sid-secret",
  "HSID":"hsid-secret",
  "SSID":"ssid-secret",
  "OSID":"osid-secret",
  "APISID":"apisid-secret",
  "SAPISID":"sapisid-secret",
  "__Secure-1PSIDTS":"optional-secret",
  "UNRELATED":"discard-me"
}`

func TestParseGoogleCookiesJSONFiltersToAllowlist(t *testing.T) {
	cookies, err := parseGoogleCookies([]byte(completeCookieJSON))
	if err != nil {
		t.Fatalf("parse cookies: %v", err)
	}
	if len(cookies) != 7 {
		t.Fatalf("cookie count = %d, want 7: %#v", len(cookies), cookies)
	}
	if cookies["SAPISID"] != "sapisid-secret" {
		t.Fatalf("missing SAPISID")
	}
	if _, ok := cookies["UNRELATED"]; ok {
		t.Fatalf("unrelated cookie was retained")
	}
}

func TestParseGoogleCookiesCopiedCurl(t *testing.T) {
	raw := `curl 'https://messages.google.com/web/config' \
  -H 'accept: */*' \
  -H 'cookie: SID=sid; HSID=hsid; SSID=ssid; OSID=osid; APISID=apisid; SAPISID=sapisid; __Secure-1PSIDTS=optional'`
	cookies, err := parseGoogleCookies([]byte(raw))
	if err != nil {
		t.Fatalf("parse cURL: %v", err)
	}
	if cookies["OSID"] != "osid" || cookies["__Secure-1PSIDTS"] != "optional" {
		t.Fatalf("unexpected parsed cookies: %#v", cookies)
	}
}

func TestParseGoogleCookiesMissingNamesDoesNotLeakValues(t *testing.T) {
	_, err := parseGoogleCookies([]byte(`{"SID":"do-not-print"}`))
	if err == nil {
		t.Fatal("expected missing-cookie error")
	}
	if strings.Contains(err.Error(), "do-not-print") {
		t.Fatalf("error leaked cookie value: %v", err)
	}
	for _, want := range []string{"APISID", "HSID", "OSID", "SAPISID", "SSID"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing cookie name %s: %v", want, err)
		}
	}
}

func TestReadCookieInputEnforcesPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	if err := os.WriteFile(path, []byte(completeCookieJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	// WriteFile honors the umask; force the insecure mode the test is about.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readCookieInput(path, bytes.NewReader(nil), false); err == nil {
		t.Fatal("expected insecure-permissions error")
	}
	if _, err := readCookieInput(path, bytes.NewReader(nil), true); err != nil {
		t.Fatalf("explicit permission override failed: %v", err)
	}
}

func TestReadCookieInputStdinAndEmptyInput(t *testing.T) {
	raw, err := readCookieInput("-", strings.NewReader(completeCookieJSON), false)
	if err != nil || !bytes.Contains(raw, []byte("SAPISID")) {
		t.Fatalf("read stdin: %q, %v", raw, err)
	}
	_, err = readCookieInput("-", strings.NewReader(" \n"), false)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("empty input error=%v exit=%d, want usage exit 2", err, ExitCode(err))
	}
}

func TestAuthHelpDescribesGaiaWithoutQR(t *testing.T) {
	cmd := authCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("auth help: %v", err)
	}
	help := out.String()
	if !strings.Contains(help, "--cookies-file") || !strings.Contains(help, "emoji") {
		t.Fatalf("Gaia help missing expected guidance: %s", help)
	}
	if strings.Contains(strings.ToLower(help), "qr") {
		t.Fatalf("dead QR flow remains in auth help: %s", help)
	}
}

func TestAuthClassifiesMalformedCookiesAsUsage(t *testing.T) {
	cmd := authCmd()
	cmd.SetIn(strings.NewReader(`{"SID":"secret"}`))
	cmd.SetArgs([]string{"--cookies-file", "-"})
	err := cmd.Execute()
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("malformed cookie error=%v exit=%d, want usage exit 2", err, ExitCode(err))
	}
	var usage usageError
	if !errors.As(err, &usage) {
		t.Fatalf("error type = %T, want usageError", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("error leaked cookie value: %v", err)
	}
}

func TestPairingInvalidatesPriorHealthWithoutNetwork(t *testing.T) {
	ctx := context.Background()
	layout, err := paths.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, kind := range []string{"starting", "data"} {
		if err := st.ObserveHealth(ctx, kind); err != nil {
			t.Fatal(err)
		}
	}
	if err := invalidatePairingHealth(ctx, layout); err != nil {
		t.Fatal(err)
	}
	h, err := st.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := h.Assessment(time.Now()); status != "unhealthy" || h.Invalidation != "session_changed" {
		t.Fatalf("prior health retained: %+v", h)
	}
}

func TestAuthWithoutFlagsStartsBrowserWithoutCookieFile(t *testing.T) {
	want := errors.New("sign-in cancelled by test")
	called := false
	cmd := authCmdWithBrowser(func(ctx context.Context, opts browserauth.Options) (map[string]string, error) {
		called = true
		if opts.BrowserPath != "" || opts.AccountHint != "" {
			t.Fatalf("default browser options = %+v", opts)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("browser sign-in has no timeout")
		}
		return nil, want
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); !errors.Is(err, want) || !called {
		t.Fatalf("default auth did not launch browser: called=%v err=%v", called, err)
	}
	if !strings.Contains(out.String(), "Sign in") || strings.Contains(out.String(), "required") {
		t.Fatalf("unexpected default instructions: %s", out.String())
	}
}

func TestAuthRejectsConflictingOptionsBeforeBrowserLaunch(t *testing.T) {
	for _, args := range [][]string{
		{"--browser", "chrome", "--cookies-file", "-"},
		{"--browser-timeout", "0s"},
		{"--account", "not-an-address"},
		{"--account", "two@at@signs"},
		{"--account", "spaced out@example.com"},
	} {
		cmd := authCmdWithBrowser(func(context.Context, browserauth.Options) (map[string]string, error) {
			t.Fatal("browser launched with invalid options")
			return nil, nil
		})
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted invalid options %v", args)
		}
	}
}

func TestMissingCookieFileSuggestsBrowserSignIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-created.json")
	_, err := readCookieInput(path, strings.NewReader(""), false)
	if err == nil || !strings.Contains(err.Error(), "run gmcli auth without --cookies-file") {
		t.Fatalf("missing file error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created an empty credential file")
	}
}

// silenceLikeRoot mirrors Root()'s SilenceUsage/SilenceErrors so a subcommand
// executed on its own does not print cobra usage text into stdout.
func silenceLikeRoot(cmd *cobra.Command) {
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
}

// decodeAuthEvents parses the NDJSON stream written by gmcli --json auth.
func decodeAuthEvents(t *testing.T, raw string) []authEvent {
	t.Helper()
	var events []authEvent
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		var ev authEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("event line is not a JSON object: %q: %v", line, err)
		}
		events = append(events, ev)
	}
	return events
}

func eventNames(events []authEvent) []string {
	names := make([]string, len(events))
	for i, ev := range events {
		names[i] = ev.Event
	}
	return names
}

func TestAuthJSONStreamsEventsThroughPairing(t *testing.T) {
	oldFlags := flags
	t.Cleanup(func() { flags = oldFlags })
	flags.storeDir = t.TempDir()
	flags.jsonOut = true

	deps := authDeps{
		signIn: func(_ context.Context, opts browserauth.Options) (map[string]string, error) {
			opts.Progress(browserauth.StageBrowserReady)
			opts.Progress(browserauth.StageSignedIn)
			return parseGoogleCookies([]byte(completeCookieJSON))
		},
		authenticate: func(_ context.Context, layout paths.Layout, _ zerolog.Logger, cookies map[string]string, opts gm.PairOptions, render gm.EmojiRenderer) (*gm.PairResult, error) {
			if cookies["SID"] != "sid-secret" || opts.ForceNew || opts.ExpectedAccount != "" {
				t.Fatalf("unexpected pairing input: cookies=%d opts=%+v", len(cookies), opts)
			}
			render("🦊")
			return &gm.PairResult{Mode: "paired", Account: "person@example.com", PhoneID: "phone-1", SessionPath: layout.Session}, nil
		},
	}
	cmd := authCmdWithDeps(deps)
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("auth --json: %v", err)
	}
	events := decodeAuthEvents(t, stdout.String())
	want := []string{"browser_starting", "browser_ready", "signed_in", "validating", "emoji", "waiting_for_phone", "complete"}
	if got := eventNames(events); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("event order = %v, want %v", got, want)
	}
	if events[4].Emoji != "🦊" {
		t.Fatalf("emoji event = %+v", events[4])
	}
	last := events[len(events)-1]
	if last.Mode != "paired" || last.Account != "person@example.com" || last.PhoneID != "phone-1" || last.SessionPath == "" {
		t.Fatalf("complete event = %+v", last)
	}
	if strings.Contains(stdout.String(), "secret") {
		t.Fatal("event stream leaked cookie values")
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("--json wrote human text to stderr: %q", stderr.String())
	}
}

func TestAuthJSONErrorEventCarriesPairingCode(t *testing.T) {
	oldFlags := flags
	t.Cleanup(func() { flags = oldFlags })
	flags.storeDir = t.TempDir()
	flags.jsonOut = true

	// A real PairError from the gm package, so the code round-trips through fmt wrapping.
	cmd := authCmdWithDeps(authDeps{
		authenticate: func(_ context.Context, _ paths.Layout, _ zerolog.Logger, _ map[string]string, _ gm.PairOptions, _ gm.EmojiRenderer) (*gm.PairResult, error) {
			return nil, gm.NewPairError(gm.CodeWrongEmoji, errors.New("the wrong emoji was selected"))
		},
	})
	silenceLikeRoot(cmd)
	cmd.SetIn(strings.NewReader(completeCookieJSON))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--cookies-file", "-"})
	err := cmd.Execute()
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("error=%v exit=%d, want operational failure", err, ExitCode(err))
	}
	events := decodeAuthEvents(t, stdout.String())
	last := events[len(events)-1]
	if last.Event != "error" || last.Code != gm.CodeWrongEmoji || !strings.Contains(last.Message, "wrong emoji") {
		t.Fatalf("error event = %+v", last)
	}
}

func TestAuthAccountFlagReachesBrowserAndPairing(t *testing.T) {
	oldFlags := flags
	t.Cleanup(func() { flags = oldFlags })
	flags.storeDir = t.TempDir()
	var hint string
	var pairOpts gm.PairOptions
	cmd := authCmdWithDeps(authDeps{
		signIn: func(_ context.Context, opts browserauth.Options) (map[string]string, error) {
			hint = opts.AccountHint
			return parseGoogleCookies([]byte(completeCookieJSON))
		},
		authenticate: func(_ context.Context, layout paths.Layout, _ zerolog.Logger, _ map[string]string, opts gm.PairOptions, _ gm.EmojiRenderer) (*gm.PairResult, error) {
			pairOpts = opts
			return &gm.PairResult{Mode: "reauthenticated", Account: "person@example.com", SessionPath: layout.Session}, nil
		},
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--account", "Person@Example.com", "--new"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if hint != "Person@Example.com" {
		t.Fatalf("browser hint = %q", hint)
	}
	if pairOpts.ExpectedAccount != "Person@Example.com" || !pairOpts.ForceNew {
		t.Fatalf("pair options = %+v", pairOpts)
	}
}

func TestAuthJSONUsageErrorsAreCodedAsUsage(t *testing.T) {
	oldFlags := flags
	t.Cleanup(func() { flags = oldFlags })
	flags.jsonOut = true
	cmd := authCmd()
	silenceLikeRoot(cmd)
	cmd.SetIn(strings.NewReader(`{"SID":"secret"}`))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--cookies-file", "-"})
	if err := cmd.Execute(); err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected usage failure, got %v", err)
	}
	events := decodeAuthEvents(t, stdout.String())
	if len(events) != 1 || events[0].Event != "error" || events[0].Code != codeUsage {
		t.Fatalf("usage error events = %+v", events)
	}
	if strings.Contains(stdout.String(), "secret") {
		t.Fatal("error event leaked cookie value")
	}
}

func TestErrorCodeClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"pair error through wrapping", fmt.Errorf("authenticate: %w", gm.NewPairError(gm.CodeNoDevices, errors.New("x"))), gm.CodeNoDevices},
		{"browser error", browserauth.NewError(browserauth.CodeBrowserTimeout, errors.New("x")), browserauth.CodeBrowserTimeout},
		{"cancelled", context.Canceled, codeCancelled},
		{"deadline", fmt.Errorf("wrapped: %w", context.DeadlineExceeded), codeTimeout},
		{"usage", usageErrorf("bad flag"), codeUsage},
		{"other", errors.New("boom"), codeFailed},
	} {
		if got := errorCode(tc.err); got != tc.want {
			t.Errorf("%s: errorCode = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestExitCodeDistinguishesLoggedOutSession(t *testing.T) {
	loggedOut := fmt.Errorf("sync: %w", gm.NewPairError(gm.CodeSessionLoggedOut, errors.New("logged out")))
	if got := ExitCode(loggedOut); got != ExitAuthRequired {
		t.Fatalf("logged-out exit = %d, want %d", got, ExitAuthRequired)
	}
	other := gm.NewPairError(gm.CodePairingInvalid, errors.New("relay"))
	if got := ExitCode(other); got != 1 {
		t.Fatalf("other pairing failure exit = %d, want 1", got)
	}
	if got := ExitCode(usageErrorf("bad")); got != 2 {
		t.Fatalf("usage exit = %d, want 2", got)
	}
}

func TestReportCommandErrorWritesOneJSONLineOnlyWithJSON(t *testing.T) {
	err := gm.NewPairError(gm.CodeSessionLoggedOut, errors.New("logged out"))
	var out bytes.Buffer
	if got := reportCommandError(&out, false, err); got != err || out.Len() != 0 {
		t.Fatalf("human mode wrote %q", out.String())
	}
	if got := reportCommandError(&out, true, nil); got != nil || out.Len() != 0 {
		t.Fatalf("nil error wrote %q", out.String())
	}
	if got := reportCommandError(&out, true, err); got != err {
		t.Fatal("error was not returned unchanged")
	}
	var line commandError
	if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &line); jsonErr != nil {
		t.Fatalf("not one JSON object: %q", out.String())
	}
	if line.Event != "error" || line.Code != gm.CodeSessionLoggedOut || line.Message != "logged out" {
		t.Fatalf("line = %+v", line)
	}
}

func TestCancelledBrowserResultDoesNotTouchSavedSession(t *testing.T) {
	oldFlags := flags
	t.Cleanup(func() { flags = oldFlags })
	flags.storeDir = t.TempDir()
	layout, err := resolveLayout()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.Session, []byte("saved-session-sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := authCmdWithBrowser(func(context.Context, browserauth.Options) (map[string]string, error) {
		cancel()
		return parseGoogleCookies([]byte(completeCookieJSON))
	})
	cmd.SetContext(ctx)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled sign-in error = %v", err)
	}
	saved, err := os.ReadFile(layout.Session)
	if err != nil || string(saved) != "saved-session-sentinel" {
		t.Fatal("cancelled browser sign-in changed saved session")
	}
	if strings.Contains(out.String(), "secret") {
		t.Fatal("browser result leaked cookie values")
	}
}
