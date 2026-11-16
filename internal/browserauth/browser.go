// Package browserauth collects Google Messages credentials from a dedicated,
// temporary browser profile after the user signs in themselves.
package browserauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

const (
	loginURL  = "https://accounts.google.com/AccountChooser?continue=https://messages.google.com/web/config"
	configURL = "https://messages.google.com/web/config"
)

// Progress stages reported by SignIn, in order.
const (
	StageBrowserReady = "browser_ready" // the sign-in page is open
	StageSignedIn     = "signed_in"     // credentials were captured; the browser is closing
)

// Event is one progress report from SignIn.
type Event struct {
	Stage string
	// DevToolsSocket and TargetID are set on StageBrowserReady when
	// Options.DevToolsSocket was requested: the Unix socket relaying Chrome's
	// DevTools endpoint and the page target a viewer should attach to.
	DevToolsSocket string
	TargetID       string
	// VirtualDisplay is true when gmcli started its own X server for the
	// browser because the environment had none.
	VirtualDisplay bool
}

// Error codes for browser sign-in failures.
const (
	CodeNoDisplay       = "no_display"        // Linux without DISPLAY or WAYLAND_DISPLAY
	CodeBrowserNotFound = "browser_not_found" // no supported browser executable
	CodeBrowserFailed   = "browser_failed"    // the browser did not start or stopped early
	CodeBrowserTimeout  = "browser_timeout"   // the sign-in deadline passed
	CodeCleanupFailed   = "cleanup_failed"    // the temporary profile could not be removed
	CodeCancelled       = "cancelled"         // the caller cancelled (Ctrl-C, SIGTERM)
)

// profileRemoveAttempts and profileRemoveDelay bound the retry loop around
// removing the temporary profile. Chrome's helper processes keep writing cache
// and network state for a moment after the main process exits, which makes a
// single RemoveAll fail with a non-empty directory.
const (
	profileRemoveAttempts = 8
	profileRemoveDelay    = 250 * time.Millisecond
)

// Error carries a stable Code for scripts; the message stays human-readable
// and never includes browser output.
type Error struct {
	code string
	err  error
}

func (e *Error) Error() string { return e.err.Error() }
func (e *Error) Unwrap() error { return e.err }

// Code returns the stable browser sign-in error code.
func (e *Error) Code() string { return e.code }

// NewError wraps err with a stable code.
func NewError(code string, err error) *Error {
	return &Error{code: code, err: err}
}

func codedErrorf(code string, format string, args ...any) error {
	return &Error{code: code, err: fmt.Errorf(format, args...)}
}

// Options configures SignIn.
type Options struct {
	// BrowserPath selects the Chrome, Chromium or Edge executable; empty
	// auto-detects.
	BrowserPath string
	// AccountHint, when set, pre-fills Google's account chooser with this
	// address so the person lands on the intended account.
	AccountHint string
	// DevToolsSocket, when set, exposes the browser's DevTools endpoint on a
	// private Unix socket at this path so a viewer can show the window and
	// forward input. Without a desktop display, a virtual X server is started
	// for the browser (Xvfb must be installed).
	DevToolsSocket string
	// Progress, when non-nil, is called with each stage as it is reached.
	Progress func(Event)
}

// signInURL returns the account chooser address, carrying the hint when set.
func signInURL(accountHint string) string {
	if accountHint == "" {
		return loginURL
	}
	u, _ := url.Parse(loginURL)
	q := u.Query()
	q.Set("Email", accountHint)
	u.RawQuery = q.Encode()
	return u.String()
}

// SignIn opens a visible Chrome/Chromium window. It never reads an existing
// browser profile, asks for a password, or writes an intermediate cookie file.
// The caller should provide a deadline for the user-controlled login step.
func SignIn(ctx context.Context, opts Options) (cookies map[string]string, err error) {
	progress := opts.Progress
	if progress == nil {
		progress = func(Event) {}
	}
	disp, err := ensureDisplay(ctx, opts.DevToolsSocket != "", exec.LookPath, defaultXvfb)
	if err != nil {
		return nil, err
	}
	defer disp.stop()
	path, err := findBrowser(opts.BrowserPath)
	if err != nil {
		return nil, err
	}
	var extra []chromedp.ExecAllocatorOption
	if len(disp.env) > 0 {
		extra = append(extra, chromedp.Env(disp.env...))
	}
	browserCtx, port, cleanup, err := newBrowserWithPort(ctx, path, extra...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			cookies = nil
			// Keep the original failure first so its code wins (a cancelled
			// sign-in stays "cancelled"), but never hide a profile left on disk.
			err = errors.Join(err, cleanupErr)
		}
	}()
	ready := Event{Stage: StageBrowserReady, VirtualDisplay: disp.virtual}
	if opts.DevToolsSocket != "" {
		relay, err := newDevToolsRelay(opts.DevToolsSocket, port)
		if err != nil {
			return nil, codedErrorf(CodeBrowserFailed, "%w", err)
		}
		defer relay.Close()
		ready.DevToolsSocket = relay.path
		if target := chromedp.FromContext(browserCtx).Target; target != nil {
			ready.TargetID = target.TargetID.String()
		}
	}
	if err := chromedp.Run(browserCtx, chromedp.Navigate(signInURL(opts.AccountHint))); err != nil {
		return nil, browserError(ctx, "could not open Google sign-in")
	}
	progress(ready)
	cookies, err = waitForCookies(browserCtx, browserSnapshot, time.Second)
	if err != nil {
		return nil, browserError(ctx, "browser closed or disconnected before sign-in completed")
	}
	progress(Event{Stage: StageSignedIn})
	return cookies, nil
}

func browserError(ctx context.Context, message string) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return codedErrorf(CodeBrowserTimeout, "browser sign-in timed out; run gmcli auth again or increase --browser-timeout: %w", ctx.Err())
	}
	if ctx.Err() != nil {
		return codedErrorf(CodeCancelled, "browser sign-in cancelled: %w", ctx.Err())
	}
	// Browser errors can include URLs, headers and process output. Never return
	// that text to logs or the terminal after a user has started signing in.
	return codedErrorf(CodeBrowserFailed, "%s; run gmcli auth again, or use --cookies-file - (see gmcli auth --help)", message)
}

func newBrowser(ctx context.Context, path string, extra ...chromedp.ExecAllocatorOption) (context.Context, func() error, error) {
	browserCtx, _, cleanup, err := newBrowserWithPort(ctx, path, extra...)
	return browserCtx, cleanup, err
}

// newBrowserWithPort starts the sign-in browser and also returns the loopback
// DevTools port it listens on, for the optional viewer relay.
func newBrowserWithPort(ctx context.Context, path string, extra ...chromedp.ExecAllocatorOption) (context.Context, int, func() error, error) {
	// Port zero puts Chromium in WebDriver mode (navigator.webdriver becomes
	// true and Google refuses the sign-in), even for a human-controlled
	// sign-in. Choose an available loopback port before starting the browser.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, 0, nil, codedErrorf(CodeBrowserFailed, "could not allocate a local sign-in browser connection")
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	profile, err := os.MkdirTemp("", "gmcli-auth-")
	if err != nil {
		return nil, 0, nil, codedErrorf(CodeBrowserFailed, "create temporary sign-in profile: %w", err)
	}
	// Use a normal, visible browser, without chromedp's automation defaults.
	// DBSC binds credentials to Chrome; libgm needs reusable cookies. These
	// switches apply only to this disposable profile, never the user's browser.
	opts := []chromedp.ExecAllocatorOption{
		chromedp.ExecPath(path), chromedp.UserDataDir(profile),
		// Keep browser-created temporary downloads inside the private cleanup scope.
		chromedp.Env("TMPDIR="+profile, "TMP="+profile, "TEMP="+profile),
		chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("disable-features", "DeviceBoundSessions,EnableBoundSessionCredentials"),
		chromedp.Flag("disable-sync", true), chromedp.Flag("disable-extensions", true),
		chromedp.Flag("no-sandbox", false),
		chromedp.Flag("remote-debugging-address", "127.0.0.1"),
		chromedp.Flag("remote-debugging-port", strconv.Itoa(port)),
		chromedp.Flag("window-size", "960,800"),
	}
	opts = append(opts, extra...)
	// The browser's own lifetime is not tied to the caller's context on
	// purpose. If it were, Ctrl-C would make chromedp kill Chrome outright, and
	// Chrome's helper processes then keep writing cache and network state into
	// the profile after it has been removed. Instead, the returned context is
	// cancelled when the caller's is, and cleanup closes the browser gracefully
	// (Browser.close) so every helper flushes and exits before removal.
	allocatorCtx, cancelAllocator := chromedp.NewExecAllocator(context.Background(), opts...)
	browserCtx, cancelBrowser := chromedp.NewContext(allocatorCtx,
		chromedp.WithLogf(func(string, ...any) {}),
		chromedp.WithErrorf(func(string, ...any) {}))
	linked, cancelLinked := context.WithCancel(browserCtx)
	go func() {
		select {
		case <-ctx.Done():
			cancelLinked()
		case <-linked.Done():
		}
	}()
	cleanup := func() error {
		cancelLinked()
		if browserCtx.Err() == nil && chromedp.FromContext(browserCtx).Browser != nil {
			closeCtx, cancelClose := context.WithTimeout(browserCtx, 5*time.Second)
			_ = chromedp.Cancel(closeCtx)
			cancelClose()
		}
		cancelBrowser()
		cancelAllocator() // wait for the process to exit before removing its data
		if err := removeAllRetry(profile, profileRemoveAttempts, profileRemoveDelay); err != nil {
			return codedErrorf(CodeCleanupFailed, "could not remove temporary sign-in profile %s; remove it manually", profile)
		}
		return nil
	}
	if err := chromedp.Run(linked); err != nil {
		if cleanupErr := cleanup(); cleanupErr != nil {
			return nil, 0, nil, cleanupErr
		}
		return nil, 0, nil, browserError(ctx, "could not start the sign-in browser; install Chrome or Chromium, or select its executable with --browser")
	}
	return linked, port, cleanup, nil
}

// removeAllRetry removes dir, retrying while late writers (browser helper
// processes shutting down) keep recreating entries. It gives up after
// attempts tries spaced by delay.
func removeAllRetry(dir string, attempts int, delay time.Duration) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
		if _, statErr := os.Lstat(dir); errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		time.Sleep(delay)
	}
	return err
}

type snapshotFunc func(context.Context) (string, map[string]string, error)

func waitForCookies(ctx context.Context, snapshot snapshotFunc, interval time.Duration) (map[string]string, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		location, values, err := snapshot(ctx)
		if err != nil {
			return nil, err
		}
		if isConfigURL(location) {
			if cookies, err := FilterCookies(values); err == nil {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return cookies, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func isConfigURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() == "messages.google.com" &&
		(u.Port() == "" || u.Port() == "443") && u.User == nil && u.Path == "/web/config"
}

func browserSnapshot(ctx context.Context) (string, map[string]string, error) {
	var location string
	var cookies []*network.Cookie
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		// Reading the frame URL avoids JavaScript execution-context errors
		// while Google redirects between sign-in and verification pages.
		frameTree, err := page.GetFrameTree().Do(ctx)
		if err != nil {
			return err
		}
		location = frameTree.Frame.URL
		if !isConfigURL(location) {
			return nil
		}
		cookies, err = network.GetCookies().WithURLs([]string{configURL}).Do(ctx)
		return err
	}))
	values := make(map[string]string)
	for _, cookie := range cookies {
		if domain, ok := cookieDomains[cookie.Name]; ok && strings.TrimPrefix(cookie.Domain, ".") == domain {
			values[cookie.Name] = cookie.Value
		}
	}
	return location, values, err
}

func findBrowser(requested string) (string, error) {
	if requested != "" {
		path, err := exec.LookPath(requested)
		if err != nil {
			return "", codedErrorf(CodeBrowserNotFound, "--browser must name an installed Chrome, Chromium or Edge executable")
		}
		return path, nil
	}
	candidates := []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "microsoft-edge-stable"}
	switch runtime.GOOS {
	case "darwin":
		for _, app := range []string{"Google Chrome", "Chromium", "Microsoft Edge"} {
			candidates = append(candidates, "/Applications/"+app+".app/Contents/MacOS/"+app)
			if home, err := os.UserHomeDir(); err == nil {
				candidates = append(candidates, filepath.Join(home, "Applications", app+".app", "Contents", "MacOS", app))
			}
		}
	case "windows":
		candidates = append(candidates, "chrome.exe", "msedge.exe")
		for _, base := range []string{os.Getenv("LOCALAPPDATA"), os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
			if base != "" {
				candidates = append(candidates, filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"), filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"))
			}
		}
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", codedErrorf(CodeBrowserNotFound, "no supported sign-in browser found; install Chrome, Chromium or Edge, select one with --browser /path/to/browser, or use --cookies-file - (see gmcli auth --help)")
}
