package browserauth

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

func testCookies() map[string]string {
	return map[string]string{"SID": "sid", "HSID": "hsid", "SSID": "ssid", "OSID": "osid", "APISID": "apisid", "SAPISID": "sapisid"}
}

func TestCaptureWaitsForConfigAndCompleteCookies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	calls := 0
	got, err := waitForCookies(ctx, func(context.Context) (string, map[string]string, error) {
		calls++
		cookies := testCookies()
		if calls == 1 {
			return "https://accounts.google.com/", cookies, nil
		}
		if calls == 2 {
			delete(cookies, "OSID")
		}
		cookies["UNRELATED"] = "discard-me"
		return configURL, cookies, nil
	}, time.Millisecond)
	if err != nil || calls != 3 || len(got) != 6 || got["OSID"] != "osid" {
		t.Fatalf("capture calls=%d count=%d err=%v", calls, len(got), err)
	}
}

func TestCaptureCancellationAndBrowserFailure(t *testing.T) {
	for _, mode := range []string{"cancel-before", "cancel-during", "closed", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if mode == "cancel-before" {
				cancel()
			}
			_, err := waitForCookies(ctx, func(context.Context) (string, map[string]string, error) {
				switch mode {
				case "cancel-before":
					t.Fatal("read browser after cancellation")
				case "cancel-during":
					cancel()
					return configURL, testCookies(), nil
				case "closed":
					return "", nil, errors.New("browser closed")
				}
				return "about:blank", nil, nil
			}, time.Millisecond)
			if err == nil {
				t.Fatal("capture succeeded after interruption")
			}
		})
	}
}

func TestConfigOriginMustMatchExactly(t *testing.T) {
	for _, raw := range []string{
		"http://messages.google.com/web/config", "https://messages.google.com.evil.test/web/config",
		"https://evil.test/messages.google.com/web/config", "https://messages.google.com:1234/web/config",
		"https://user@messages.google.com/web/config", "https://messages.google.com/web/",
	} {
		if isConfigURL(raw) {
			t.Errorf("accepted unrelated URL %q", raw)
		}
	}
	if !isConfigURL(configURL + "?hl=en") {
		t.Fatal("rejected config URL with query")
	}
}

func TestMissingBrowserGivesActionableError(t *testing.T) {
	_, err := findBrowser("/nonexistent/gmcli-test-browser")
	if err == nil || !strings.Contains(err.Error(), "--browser") {
		t.Fatalf("missing browser error: %v", err)
	}
}

func TestBrowserStartupFailureCleansProfile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := newBrowser(ctx, "/nonexistent/gmcli-test-browser")
	if err == nil || !strings.Contains(err.Error(), "could not start") {
		t.Fatalf("startup error: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary profile remains after failure: count=%d err=%v", len(entries), err)
	}
}

// This uses a real browser against intercepted, synthetic Google pages. No
// Google account or phone is used. Opt in with GMCLI_BROWSER_TEST=1.
func TestBrowserCaptureIntegration(t *testing.T) {
	if os.Getenv("GMCLI_BROWSER_TEST") != "1" {
		t.Skip("set GMCLI_BROWSER_TEST=1 to exercise a local Chrome/Chromium browser")
	}
	path, err := findBrowser("")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var startup bytes.Buffer
	browserCtx, cleanup, err := newBrowser(ctx, path, chromedp.Headless, chromedp.Flag("disable-background-networking", true), chromedp.CombinedOutput(&startup))
	if err != nil {
		t.Fatalf("%v; fixture browser startup: %s", err, startup.String())
	}
	defer cleanup()
	entries, err := os.ReadDir(root)
	var profiles []os.DirEntry
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "gmcli-auth-") {
			profiles = append(profiles, entry)
		}
	}
	if err != nil || len(profiles) != 1 {
		t.Fatalf("expected one isolated profile, count=%d err=%v", len(entries), err)
	}
	info, err := profiles[0].Info()
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("temporary profile must be private: %v", err)
	}
	// Intercept all navigation before visiting the Google-shaped test URL.
	chromedp.ListenTarget(browserCtx, func(event any) {
		if paused, ok := event.(*fetch.EventRequestPaused); ok {
			go func() {
				_ = chromedp.Run(browserCtx, fetch.FulfillRequest(paused.RequestID, 200).
					WithResponseHeaders([]*fetch.HeaderEntry{{Name: "Content-Type", Value: "text/html"}}).
					WithBody("PGh0bWw+U2lnbmVkIGluPC9odG1sPg=="))
			}()
		}
	})
	actions := []chromedp.Action{fetch.Enable()}
	for name, domain := range cookieDomains {
		if domain == "google.com" {
			domain = ".google.com"
		}
		actions = append(actions, network.SetCookie(name, "fixture-"+name).WithDomain(domain).WithPath("/").WithSecure(true).WithHTTPOnly(true))
	}
	// Ignore unrelated credentials and a same-name cookie from the wrong domain.
	actions = append(actions,
		network.SetCookie("UNRELATED", "discard-me").WithDomain(".google.com").WithPath("/"),
		network.SetCookie("SID", "wrong-domain").WithDomain("messages.google.com").WithPath("/"),
		chromedp.Navigate(configURL))
	if err := chromedp.Run(browserCtx, actions...); err != nil {
		t.Fatal(err)
	}
	got, err := waitForCookies(browserCtx, browserSnapshot, time.Millisecond)
	if err != nil || len(got) != 7 || got["SID"] != "fixture-SID" || got["OSID"] != "fixture-OSID" {
		t.Fatalf("browser capture failed: count=%d err=%v", len(got), err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary profile remains after success: count=%d err=%v", len(entries), err)
	}
}
