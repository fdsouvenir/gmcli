package cmd

import (
	"context"
	"testing"
	"time"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"google.golang.org/protobuf/proto"

	"github.com/fdsouvenir/gmcli/internal/store"
)

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
