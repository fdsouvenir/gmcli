package cmd

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"google.golang.org/protobuf/proto"

	"github.com/fdsouvenir/gmcli/internal/store"
)

func TestDoctorAuthIdentityFields(t *testing.T) {
	tests := []struct {
		name      string
		gaia      bool
		wantMode  string
		wantPhone string
		wantEmail string
	}{
		{name: "Gaia", gaia: true, wantMode: "gaia", wantEmail: "person@example.com"},
		{name: "legacy QR", wantMode: "legacy_qr", wantPhone: "phone-id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			auth := libgm.NewAuthData()
			auth.Browser = &gmproto.Device{SourceID: "browser"}
			auth.Mobile = &gmproto.Device{SourceID: tc.wantPhone}
			if tc.gaia {
				auth.Mobile.SourceID = tc.wantEmail
				auth.DestRegID = uuid.New()
				auth.Cookies = map[string]string{"SID": "secret"}
			}

			report := doctorReport{Paired: true}
			populateDoctorAuthIdentity(&report, auth)
			if report.AuthMode != tc.wantMode || report.PhoneID != tc.wantPhone || report.Account != tc.wantEmail {
				t.Fatalf("unexpected identity fields: %+v", report)
			}
		})
	}
}

func TestRunDoctorReportsLastSyncActivityTime(t *testing.T) {
	oldFlags := flags
	t.Cleanup(func() { flags = oldFlags })
	flags = globalFlags{storeDir: t.TempDir(), readOnly: true}

	layout, err := resolveLayout()
	if err != nil {
		t.Fatalf("layout: %v", err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, layout.Database)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	eventTime := time.UnixMilli(1_000_000)
	connectTime := time.UnixMilli(2_000_000)
	if err := st.MarkSync(ctx, eventTime, connectTime); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := st.TouchSync(ctx); err != nil {
		t.Fatal(err)
	}
	settings := &gmproto.Settings{
		SIMCards: []*gmproto.SIMCard{{
			SIMParticipant: &gmproto.SIMParticipant{ID: "sender-1"},
			SIMData: &gmproto.SIMData{
				SIMPayload: &gmproto.SIMPayload{Two: 1, SIMNumber: 1},
			},
		}},
		RCSSettings: &gmproto.RCSSettings{IsDefaultSMSApp: true},
	}
	raw, err := proto.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SavePhoneSettings(ctx, raw, len(settings.GetSIMCards())); err != nil {
		t.Fatal(err)
	}
	cachedSettings, err := st.LatestPhoneSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state, err := st.SyncState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	report := runDoctor(ctx)
	if !report.LastEventTime.Equal(eventTime) {
		t.Fatalf("last event: got %v want %v", report.LastEventTime, eventTime)
	}
	if !report.LastConnectTime.Equal(connectTime) {
		t.Fatalf("last connect: got %v want %v", report.LastConnectTime, connectTime)
	}
	if !report.LastSyncActivityTime.Equal(state.UpdatedAt) {
		t.Fatalf("last sync activity: got %v want %v", report.LastSyncActivityTime, state.UpdatedAt)
	}
	if !report.SendSettingsCached {
		t.Fatalf("expected send settings cached")
	}
	if report.SendSettingsSIMCount != 1 {
		t.Fatalf("send settings SIM count: got %d want 1", report.SendSettingsSIMCount)
	}
	if !report.SendSettingsUpdated.Equal(cachedSettings.UpdatedAt) {
		t.Fatalf("send settings updated: got %v want %v", report.SendSettingsUpdated, cachedSettings.UpdatedAt)
	}
	if report.SendSettingsDefault == nil || !*report.SendSettingsDefault {
		t.Fatalf("send settings default SMS app: got %v want true", report.SendSettingsDefault)
	}
}

func TestDoctorPairingModeUsesAccountIdentity(t *testing.T) {
	oldFlags := flags
	t.Cleanup(func() { flags = oldFlags })
	for _, mode := range []string{"legacy_qr", "gaia"} {
		t.Run(mode, func(t *testing.T) {
			flags = globalFlags{storeDir: t.TempDir(), readOnly: true}
			layout, err := resolveLayout()
			if err != nil {
				t.Fatal(err)
			}
			auth := libgm.NewAuthData()
			auth.Browser = &gmproto.Device{}
			if mode == "gaia" {
				auth.DestRegID = uuid.New()
			}
			// Upstream HasCookies is also true for QR accounts; it is not a mode predicate.
			data, err := json.Marshal(auth)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(layout.Session, data, 0600); err != nil {
				t.Fatal(err)
			}
			if got := runDoctor(context.Background()).PairingMode; got != mode {
				t.Fatalf("got pairing mode %s, want %s", got, mode)
			}
		})
	}
}

func TestDoctorJSONIssuesReturnFailure(t *testing.T) {
	oldFlags := flags
	t.Cleanup(func() { flags = oldFlags })
	oldOut := os.Stdout
	out, err := os.CreateTemp(t.TempDir(), "doctor-output")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = out
	t.Cleanup(func() { os.Stdout = oldOut; out.Close() })
	root := Root()
	root.SetArgs([]string{"--store", t.TempDir(), "--json", "doctor"})
	if err := root.Execute(); err == nil {
		t.Fatal("JSON doctor returned success with issues")
	}
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var r doctorReport
	if err := json.NewDecoder(out).Decode(&r); err != nil {
		t.Fatal(err)
	}
	if r.HealthStatus != "unknown" || len(r.Issues) == 0 {
		t.Fatalf("false green: %+v", r)
	}
}

func TestDoctorReportsEvidenceWithoutMessageAgeGuess(t *testing.T) {
	oldFlags := flags
	t.Cleanup(func() { flags = oldFlags })
	flags = globalFlags{storeDir: t.TempDir(), readOnly: true}
	layout, err := resolveLayout()
	if err != nil {
		t.Fatal(err)
	}
	auth := libgm.NewAuthData()
	auth.Browser = &gmproto.Device{}
	data, err := json.Marshal(auth)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.Session, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.TouchSync(ctx); err != nil {
		t.Fatal(err)
	}
	if got := runDoctor(ctx).HealthStatus; got != "unknown" {
		t.Fatalf("legacy false positive: %s", got)
	}
	for _, kind := range []string{"starting", "data"} {
		if err := st.ObserveHealth(ctx, kind); err != nil {
			t.Fatal(err)
		}
	}
	if got := runDoctor(ctx).HealthStatus; got != "recently_verified" {
		t.Fatalf("fresh evidence: %s", got)
	}
	if err := st.ObserveHealth(ctx, "fatal"); err != nil {
		t.Fatal(err)
	}
	if got := runDoctor(ctx).HealthStatus; got != "unhealthy" {
		t.Fatalf("terminal failure: %s", got)
	}
}
