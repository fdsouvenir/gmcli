package cmd

import (
	"bytes"
	"context"
	"errors"
	"github.com/fdsouvenir/gmcli/internal/paths"
	"github.com/fdsouvenir/gmcli/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
