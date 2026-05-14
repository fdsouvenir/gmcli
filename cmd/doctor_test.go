package cmd

import (
	"context"
	"testing"
	"time"

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
}
