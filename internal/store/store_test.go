package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fdsouvenir/gmcli/internal/store"
)

func openTempStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "gmcli.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestOpenMigratesFreshDB(t *testing.T) {
	st := openTempStore(t)
	ctx := context.Background()
	if n, err := st.CountConversations(ctx); err != nil || n != 0 {
		t.Fatalf("fresh db: count=%d err=%v", n, err)
	}
	state, err := st.SyncState(ctx)
	if err != nil {
		t.Fatalf("sync state: %v", err)
	}
	if state.LastEventTime.UnixMilli() != 0 || state.LastConnectTime.UnixMilli() != 0 {
		t.Fatalf("expected unset (epoch) sync timestamps, got %+v", state)
	}
	if v, err := st.SchemaVersion(ctx); err != nil || v != 3 {
		t.Fatalf("schema version: got %d err=%v, want 3", v, err)
	}
}

func TestUpsertConversationIdempotent(t *testing.T) {
	st := openTempStore(t)
	ctx := context.Background()
	c := store.Conversation{
		ID:                "conv-1",
		Name:              "Alice",
		IsGroup:           false,
		LastMessageTimeMS: 1700000000000,
		Unread:            true,
	}
	for i := 0; i < 3; i++ {
		if err := st.UpsertConversation(ctx, c); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}
	got, err := st.GetConversation(ctx, "conv-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Alice" || !got.Unread || got.LastMessageTimeMS != 1700000000000 {
		t.Fatalf("unexpected row: %+v", got)
	}

	// Older last_message_ts must not regress the stored value.
	c.LastMessageTimeMS = 1600000000000
	if err := st.UpsertConversation(ctx, c); err != nil {
		t.Fatalf("regress upsert: %v", err)
	}
	got, _ = st.GetConversation(ctx, "conv-1")
	if got.LastMessageTimeMS != 1700000000000 {
		t.Fatalf("last_message_ts regressed to %d", got.LastMessageTimeMS)
	}
}

func TestUpsertMessageAndFTSRoundTrip(t *testing.T) {
	st := openTempStore(t)
	ctx := context.Background()
	if err := st.UpsertConversation(ctx, store.Conversation{ID: "conv-1", Name: "Alice"}); err != nil {
		t.Fatalf("conv: %v", err)
	}

	body1 := "Want to grab dinner tonight?"
	body2 := "Sure, dinner sounds great"
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("upsert message: %v", err)
		}
	}
	must(st.UpsertMessage(ctx, store.Message{
		ID: "m1", ConversationID: "conv-1", Body: &body1,
		TimestampMS: time.Now().UnixMilli(),
	}))
	must(st.UpsertMessage(ctx, store.Message{
		ID: "m2", ConversationID: "conv-1", Body: &body2,
		TimestampMS: time.Now().UnixMilli() + 1, IsFromMe: true,
	}))
	must(st.UpsertMessage(ctx, store.Message{
		ID: "m3", ConversationID: "conv-2", Body: &body2,
		TimestampMS: time.Now().UnixMilli() + 2,
	}))

	if n, err := st.CountMessagesForConversation(ctx, "conv-1"); err != nil || n != 2 {
		t.Fatalf("count conv-1: got %d err=%v, want 2", n, err)
	}
	if n, err := st.CountMessagesForConversation(ctx, "conv-2"); err != nil || n != 1 {
		t.Fatalf("count conv-2: got %d err=%v, want 1", n, err)
	}

	hits, err := st.SearchMessages(ctx, "dinner", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("expected 3 hits, got %d", len(hits))
	}
	for _, h := range hits {
		if h.Snippet == "" {
			t.Errorf("empty snippet on hit %s", h.MessageID)
		}
	}

	// Idempotent re-upsert (same id).
	must(st.UpsertMessage(ctx, store.Message{
		ID: "m1", ConversationID: "conv-1", Body: &body1,
		TimestampMS: time.Now().UnixMilli(),
	}))
	hits, _ = st.SearchMessages(ctx, "dinner", 10)
	if len(hits) != 3 {
		t.Fatalf("FTS duplicated rows on re-upsert: %d hits", len(hits))
	}
}

func TestUpsertContactIdempotent(t *testing.T) {
	st := openTempStore(t)
	ctx := context.Background()
	c := store.Contact{
		ParticipantID:   "p-1",
		Name:            "Alice Example",
		E164:            "+15555550100",
		FormattedNumber: "(555) 555-0100",
	}
	for i := 0; i < 2; i++ {
		if err := st.UpsertContact(ctx, c); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}
	n, err := st.CountContacts(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 contact, got %d", n)
	}
}

func TestMarkSyncMonotonic(t *testing.T) {
	st := openTempStore(t)
	ctx := context.Background()
	t1 := time.UnixMilli(1_000_000)
	t2 := time.UnixMilli(2_000_000)
	if err := st.MarkSync(ctx, t2, t2); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSync(ctx, t1, t1); err != nil {
		t.Fatal(err)
	}
	state, err := st.SyncState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastEventTime.Equal(t2) {
		t.Fatalf("last_event_ts regressed: %v", state.LastEventTime)
	}
	if !state.LastConnectTime.Equal(t2) {
		t.Fatalf("last_connect_ts regressed: %v", state.LastConnectTime)
	}
}

func TestTouchSyncUpdatesActivityOnly(t *testing.T) {
	st := openTempStore(t)
	ctx := context.Background()
	eventTime := time.UnixMilli(1_000_000)
	connectTime := time.UnixMilli(2_000_000)
	if err := st.MarkSync(ctx, eventTime, connectTime); err != nil {
		t.Fatal(err)
	}
	before, err := st.SyncState(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var after store.SyncState
	for i := 0; i < 20; i++ {
		time.Sleep(2 * time.Millisecond)
		if err := st.TouchSync(ctx); err != nil {
			t.Fatal(err)
		}
		after, err = st.SyncState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if after.UpdatedAt.After(before.UpdatedAt) {
			break
		}
	}

	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("updated_at did not advance: before=%v after=%v", before.UpdatedAt, after.UpdatedAt)
	}
	if !after.LastEventTime.Equal(eventTime) {
		t.Fatalf("last_event_ts changed: %v", after.LastEventTime)
	}
	if !after.LastConnectTime.Equal(connectTime) {
		t.Fatalf("last_connect_ts changed: %v", after.LastConnectTime)
	}
}

func TestPhoneSettingsRoundTrip(t *testing.T) {
	st := openTempStore(t)
	ctx := context.Background()

	if _, err := st.LatestPhoneSettings(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound before save, got %v", err)
	}

	raw := []byte{1, 2, 3, 4}
	if err := st.SavePhoneSettings(ctx, raw, 2); err != nil {
		t.Fatalf("save phone settings: %v", err)
	}
	got, err := st.LatestPhoneSettings(ctx)
	if err != nil {
		t.Fatalf("latest phone settings: %v", err)
	}
	if string(got.RawProto) != string(raw) {
		t.Fatalf("raw proto mismatch: got %v want %v", got.RawProto, raw)
	}
	if got.SIMCount != 2 {
		t.Fatalf("sim count: got %d want 2", got.SIMCount)
	}
	if got.UpdatedAt.UnixMilli() <= 0 {
		t.Fatalf("updated_at was not set: %v", got.UpdatedAt)
	}
}
