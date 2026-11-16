package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/fdsouvenir/gmcli/internal/gm"
	"github.com/fdsouvenir/gmcli/internal/paths"
	"github.com/fdsouvenir/gmcli/internal/store"
)

func writeGaiaSession(t *testing.T, layout paths.Layout, account string) {
	t.Helper()
	auth := libgm.NewAuthData()
	auth.Browser = &gmproto.Device{SourceID: account}
	auth.Mobile = &gmproto.Device{SourceID: account}
	auth.DestRegID = uuid.New()
	auth.PairingID = uuid.New()
	auth.Cookies = map[string]string{"SID": "sid-secret"}
	raw, err := json.Marshal(auth)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.Session, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runSubcommand(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	silenceLikeRoot(cmd)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), err
}

func TestAuthStatusOfflineDoesNotCreateArchive(t *testing.T) {
	oldFlags := flags
	t.Cleanup(func() { flags = oldFlags })
	flags = globalFlags{storeDir: t.TempDir(), jsonOut: true}
	layout, _ := paths.Resolve(flags.storeDir)
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	writeGaiaSession(t, layout, "person@example.com")

	out, err := runSubcommand(t, authStatusCmdWithValidator(func(context.Context, paths.Layout, zerolog.Logger) error {
		t.Fatal("offline status connected")
		return nil
	}))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var report authStatusReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &report); err != nil {
		t.Fatalf("status output is not one JSON object: %q: %v", out, err)
	}
	if report.Verdict != "paired" || report.Mode != "gaia" || report.Account != "person@example.com" || report.Online != nil {
		t.Fatalf("report = %+v", report)
	}
	if report.UpdatedAt.IsZero() {
		t.Fatal("missing session timestamp")
	}
	if strings.Contains(out, "secret") {
		t.Fatal("status leaked a cookie value")
	}
	if _, err := os.Stat(layout.Database); !os.IsNotExist(err) {
		t.Fatal("auth status created or opened the SQLite archive")
	}
}

func TestAuthStatusUnpairedExitsNonZeroWithCode(t *testing.T) {
	oldFlags := flags
	t.Cleanup(func() { flags = oldFlags })
	flags = globalFlags{storeDir: t.TempDir(), jsonOut: true}

	out, err := runSubcommand(t, authStatusCmd())
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("missing session: err=%v exit=%d", err, ExitCode(err))
	}
	var report authStatusReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &report); err != nil {
		t.Fatal(err)
	}
	if report.Verdict != "unpaired" || report.Code != gm.CodeNoSession || report.Exists {
		t.Fatalf("report = %+v", report)
	}
	if strings.Contains(out, "session_updated_at") {
		t.Fatalf("zero timestamp was emitted: %s", out)
	}
}

func TestAuthStatusOnlineReportsPhoneVerdict(t *testing.T) {
	for _, tc := range []struct {
		name     string
		validate error
		wantOK   bool
		wantCode string
	}{
		{"answered", nil, true, ""},
		{"logged out", gm.NewPairError(gm.CodeSessionLoggedOut, errors.New("logged out")), false, gm.CodeSessionLoggedOut},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldFlags := flags
			t.Cleanup(func() { flags = oldFlags })
			flags = globalFlags{storeDir: t.TempDir(), jsonOut: true}
			layout, _ := paths.Resolve(flags.storeDir)
			if err := layout.EnsureDirs(); err != nil {
				t.Fatal(err)
			}
			writeGaiaSession(t, layout, "person@example.com")
			called := false
			out, err := runSubcommand(t, authStatusCmdWithValidator(func(ctx context.Context, _ paths.Layout, _ zerolog.Logger) error {
				called = true
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("online check has no deadline")
				}
				return tc.validate
			}), "--online")
			if !called {
				t.Fatal("--online did not validate")
			}
			if (err == nil) != tc.wantOK {
				t.Fatalf("err=%v wantOK=%v", err, tc.wantOK)
			}
			var report authStatusReport
			if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &report); err != nil {
				t.Fatal(err)
			}
			if report.Verdict != "paired" || report.Online == nil || report.Online.OK != tc.wantOK || report.Online.Code != tc.wantCode {
				t.Fatalf("report = %+v online=%+v", report, report.Online)
			}
		})
	}
}

func TestAuthStatusHumanOutput(t *testing.T) {
	oldFlags := flags
	t.Cleanup(func() { flags = oldFlags })
	flags = globalFlags{storeDir: t.TempDir()}
	layout, _ := paths.Resolve(flags.storeDir)
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	writeGaiaSession(t, layout, "person@example.com")
	out, err := runSubcommand(t, authStatusCmd())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"verdict:   paired", "mode:      gaia", "account:   person@example.com", "updated:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("human output missing %q:\n%s", want, out)
		}
	}
}

func TestAuthRemoveReportsAndInvalidatesHealth(t *testing.T) {
	oldFlags := flags
	t.Cleanup(func() { flags = oldFlags })
	flags = globalFlags{storeDir: t.TempDir(), jsonOut: true}
	layout, _ := paths.Resolve(flags.storeDir)
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	writeGaiaSession(t, layout, "person@example.com")
	ctx := context.Background()
	st, err := store.Open(ctx, layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"starting", "data"} {
		if err := st.ObserveHealth(ctx, kind); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	var gotLocalOnly bool
	out, err := runSubcommand(t, authRemoveCmdWithRemover(func(_ context.Context, l paths.Layout, _ zerolog.Logger, localOnly bool) (*gm.RemoveResult, error) {
		gotLocalOnly = localOnly
		if err := os.Remove(l.Session); err != nil {
			t.Fatal(err)
		}
		return &gm.RemoveResult{SessionPath: l.Session, Mode: "gaia", Account: "person@example.com", PhoneUnpaired: true}, nil
	}), "--local-only")
	if err != nil {
		t.Fatal(err)
	}
	if !gotLocalOnly {
		t.Fatal("--local-only not passed through")
	}
	var result struct {
		Event         string `json:"event"`
		Account       string `json:"account"`
		PhoneUnpaired bool   `json:"phone_unpaired"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &result); err != nil {
		t.Fatal(err)
	}
	if result.Event != "removed" || result.Account != "person@example.com" || !result.PhoneUnpaired {
		t.Fatalf("result = %+v", result)
	}
	st, err = store.Open(ctx, layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h, err := st.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h.Invalidation != "session_changed" {
		t.Fatalf("health not invalidated: %+v", h)
	}
}

func TestAuthRemoveDoesNotCreateArchiveAndReportsErrors(t *testing.T) {
	oldFlags := flags
	t.Cleanup(func() { flags = oldFlags })
	flags = globalFlags{storeDir: t.TempDir(), jsonOut: true}
	layout, _ := paths.Resolve(flags.storeDir)

	out, err := runSubcommand(t, authRemoveCmdWithRemover(func(context.Context, paths.Layout, zerolog.Logger, bool) (*gm.RemoveResult, error) {
		return nil, gm.NewPairError(gm.CodeUnpairFailed, errors.New("relay rejected"))
	}))
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("err=%v", err)
	}
	events := decodeAuthEvents(t, out)
	if len(events) != 1 || events[0].Event != "error" || events[0].Code != gm.CodeUnpairFailed {
		t.Fatalf("events = %+v", events)
	}
	if _, statErr := os.Stat(layout.Database); !os.IsNotExist(statErr) {
		t.Fatal("auth remove created the SQLite archive")
	}

	out, err = runSubcommand(t, authRemoveCmdWithRemover(func(_ context.Context, l paths.Layout, _ zerolog.Logger, _ bool) (*gm.RemoveResult, error) {
		return &gm.RemoveResult{SessionPath: l.Session}, nil
	}))
	if err != nil || !strings.Contains(out, `"event":"removed"`) {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if _, statErr := os.Stat(layout.Database); !os.IsNotExist(statErr) {
		t.Fatal("auth remove created the SQLite archive on success")
	}
}

func TestAuthSubcommandsAreWired(t *testing.T) {
	root := Root()
	for _, path := range [][]string{{"auth", "status"}, {"auth", "remove"}} {
		if c, _, err := root.Find(path); err != nil || c.Name() != path[1] {
			t.Fatalf("%v not wired: %v", path, err)
		}
	}
}
