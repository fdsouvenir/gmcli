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

// SignIn opens a visible Chrome/Chromium window. It never reads an existing
// browser profile, asks for a password, or writes an intermediate cookie file.
// The caller should provide a deadline for the user-controlled login step.
func SignIn(ctx context.Context, browserPath string) (cookies map[string]string, err error) {
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return nil, errors.New("browser sign-in needs a desktop display; run gmcli auth on your desktop, or use --cookies-file - for piped input on this server (see gmcli auth --help)")
	}
	path, err := findBrowser(browserPath)
	if err != nil {
		return nil, err
	}
	browserCtx, cleanup, err := newBrowser(ctx, path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			cookies = nil
			err = cleanupErr
		}
	}()
	if err := chromedp.Run(browserCtx, chromedp.Navigate(loginURL)); err != nil {
		return nil, browserError(ctx, "could not open Google sign-in")
	}
	cookies, err = waitForCookies(browserCtx, browserSnapshot, time.Second)
	if err != nil {
		return nil, browserError(ctx, "browser closed or disconnected before sign-in completed")
	}
	return cookies, nil
}

func browserError(ctx context.Context, message string) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("browser sign-in timed out; run gmcli auth again or increase --browser-timeout: %w", ctx.Err())
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Browser errors can include URLs, headers and process output. Never return
	// that text to logs or the terminal after a user has started signing in.
	return fmt.Errorf("%s; run gmcli auth again, or use --cookies-file - (see gmcli auth --help)", message)
}

func newBrowser(ctx context.Context, path string, extra ...chromedp.ExecAllocatorOption) (context.Context, func() error, error) {
	// Port zero puts Chromium in WebDriver mode, even for a human-controlled
	// sign-in. Choose an available loopback port before starting the browser.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, nil, errors.New("could not allocate a local sign-in browser connection")
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	profile, err := os.MkdirTemp("", "gmcli-auth-")
	if err != nil {
		return nil, nil, fmt.Errorf("create temporary sign-in profile: %w", err)
	}
	// Use a normal, visible browser, without chromedp's automation defaults.
	// DBSC binds credentials to Chrome; libgm needs reusable cookies. These
	// switches apply only to this disposable profile, never the user's browser.
	opts := []chromedp.ExecAllocatorOption{
		chromedp.ExecPath(path), chromedp.UserDataDir(profile),
		chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("disable-features", "DeviceBoundSessions,EnableBoundSessionCredentials"),
		chromedp.Flag("disable-sync", true), chromedp.Flag("disable-extensions", true),
		chromedp.Flag("no-sandbox", false),
		chromedp.Flag("remote-debugging-address", "127.0.0.1"),
		chromedp.Flag("remote-debugging-port", strconv.Itoa(port)),
		chromedp.Flag("window-size", "960,800"),
	}
	opts = append(opts, extra...)
	allocatorCtx, cancelAllocator := chromedp.NewExecAllocator(ctx, opts...)
	browserCtx, cancelBrowser := chromedp.NewContext(allocatorCtx,
		chromedp.WithLogf(func(string, ...any) {}),
		chromedp.WithErrorf(func(string, ...any) {}))
	cleanup := func() error {
		if browserCtx.Err() == nil && chromedp.FromContext(browserCtx).Browser != nil {
			closeCtx, cancelClose := context.WithTimeout(browserCtx, 3*time.Second)
			_ = chromedp.Cancel(closeCtx)
			cancelClose()
		}
		cancelBrowser()
		cancelAllocator() // wait for the process to exit before removing its data
		if err := os.RemoveAll(profile); err != nil {
			return fmt.Errorf("could not remove temporary sign-in profile %s; remove it manually", profile)
		}
		return nil
	}
	if err := chromedp.Run(browserCtx); err != nil {
		if cleanupErr := cleanup(); cleanupErr != nil {
			return nil, nil, cleanupErr
		}
		return nil, nil, browserError(ctx, "could not start the sign-in browser; install Chrome or Chromium, or select its executable with --browser")
	}
	return browserCtx, cleanup, nil
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
			return "", errors.New("--browser must name an installed Chrome, Chromium or Edge executable")
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
	return "", errors.New("no supported sign-in browser found; install Chrome, Chromium or Edge, select one with --browser /path/to/browser, or use --cookies-file - (see gmcli auth --help)")
}
