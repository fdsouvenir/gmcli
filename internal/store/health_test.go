package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func healthStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func observe(t *testing.T, s *Store, kinds ...string) {
	t.Helper()
	for _, k := range kinds {
		if e := s.ObserveHealth(context.Background(), k); e != nil {
			t.Fatal(e)
		}
	}
}
func status(t *testing.T, s *Store, want string) {
	t.Helper()
	h, e := s.Health(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if got, issue := h.Assessment(time.Now()); got != want {
		t.Fatalf("got %s want %s: %s %+v", got, want, issue, h)
	}
}
func TestHealthLegacyMigrationAndQuietInbox(t *testing.T) {
	s := healthStore(t)
	ctx := context.Background()
	if _, e := s.db.Exec(`DROP TABLE connection_health;DELETE FROM schema_version WHERE version=4`); e != nil {
		t.Fatal(e)
	}
	if e := s.TouchSync(ctx); e != nil {
		t.Fatal(e)
	}
	if e := s.migrate(ctx); e != nil {
		t.Fatal(e)
	}
	status(t, s, "unknown")
	observe(t, s, "starting", "transport", "phone")
	status(t, s, "unknown")
	if _, e := s.ObserveSnapshot(ctx, "conversations", 0); e != nil {
		t.Fatal(e)
	}
	status(t, s, "recently_verified") // explicit valid empty snapshot, empty archive
	h, _ := s.Health(ctx)
	observe(t, s, "heartbeat")
	after, _ := s.Health(ctx)
	if h.DataEvidence != after.DataEvidence || h.PhoneResponse != after.PhoneResponse || h.SnapshotVerified != after.SnapshotVerified {
		t.Fatal("synthetic evidence")
	}
}
func TestHealthPopulatedArchiveEmptySnapshot(t *testing.T) {
	s := healthStore(t)
	ctx := context.Background()
	tx, e := s.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		if _, e = tx.Exec(`INSERT INTO conversations(conversation_id,updated_at) VALUES(?,0)`, fmt.Sprint(i)); e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 7; i++ {
		if _, e = tx.Exec(`INSERT INTO messages(message_id,conversation_id,timestamp_ms,updated_at) VALUES(?,'0',1,0)`, fmt.Sprint(i)); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	observe(t, s, "starting", "transport")
	mismatch, e := s.ObserveSnapshot(ctx, "conversations", 0)
	if e != nil || !mismatch {
		t.Fatal("missed populated archive mismatch", e)
	}
	observe(t, s, "phone", "heartbeat", "data")
	status(t, s, "unknown")
	if _, e = s.ObserveSnapshot(ctx, "contacts", 1); e != nil {
		t.Fatal(e)
	}
	status(t, s, "unknown")
	// Actual nonempty conversation snapshot repairs the discrepancy in the same run.
	if _, e = s.ObserveSnapshot(ctx, "conversations", 3); e != nil {
		t.Fatal(e)
	}
	status(t, s, "recently_verified")
}
func TestHealthRecoverableAndTerminalFailures(t *testing.T) {
	s := healthStore(t)
	ctx := context.Background()
	observe(t, s, "starting", "data")
	status(t, s, "recently_verified")
	observe(t, s, "no_data", "phone")
	status(t, s, "unknown")
	observe(t, s, "data")
	status(t, s, "recently_verified")
	observe(t, s, "temporary_error", "transport")
	status(t, s, "unknown")
	observe(t, s, "phone")
	status(t, s, "unknown") // old archive proof was invalidated
	observe(t, s, "data")
	status(t, s, "recently_verified")
	for _, failure := range []string{"auth_invalid", "fatal", "disconnected", "session_changed"} {
		observe(t, s, "starting", "data", failure)
		before, _ := s.Health(ctx)
		observe(t, s, "phone", "data", "transport", "protocol", "heartbeat", "no_data")
		if _, e := s.ObserveSnapshot(ctx, "conversations", 1); e != nil {
			t.Fatal(e)
		}
		after, _ := s.Health(ctx)
		if before != after {
			t.Fatalf("late event changed terminal health: before=%+v after=%+v", before, after)
		}
		status(t, s, "unhealthy")
	}
}
func TestHealthAssessmentFreshness(t *testing.T) {
	now := time.Now()
	ms := now.UnixMilli()
	h := Health{ProcessHeartbeat: ms, Transport: "observed", ProtocolEvidence: ms, PhoneResponse: ms, DataEvidence: ms, Phone: "responding"}
	if got, _ := h.Assessment(now); got != "recently_verified" {
		t.Fatal(got)
	}
	h.ProcessHeartbeat = now.Add(16 * time.Minute).UnixMilli()
	if got, _ := h.Assessment(now.Add(16 * time.Minute)); got != "unknown" {
		t.Fatal(got)
	}
	if got, _ := h.Assessment(now.Add(-time.Minute)); got != "unknown" {
		t.Fatal(got)
	}
}

func TestHealthMigrationPreservesArchiveAndSchema4Evidence(t *testing.T) {
	s := healthStore(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`DROP TABLE connection_health; DELETE FROM schema_version WHERE version=4;
 INSERT INTO conversations(conversation_id,updated_at) VALUES('conversation',0);
 INSERT INTO messages(message_id,conversation_id,timestamp_ms,updated_at) VALUES('message','conversation',1,0);`); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchSync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	status(t, s, "unknown")
	if n, err := s.CountMessages(ctx); err != nil || n != 1 {
		t.Fatalf("archive changed: %d %v", n, err)
	}
	var integrity string
	if err := s.db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity: %s %v", integrity, err)
	}
	observe(t, s, "starting", "data")
	if _, err := s.ObserveSnapshot(ctx, "conversations", 0); err != nil {
		t.Fatal(err)
	}
	before, err := s.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// An already-migrated full schema-4 store must retain every evidence field.
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("schema-4 evidence changed: before=%+v after=%+v", before, after)
	}
}

func TestHealthContactMismatchRequiresMatchingSnapshot(t *testing.T) {
	s := healthStore(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`INSERT INTO contacts(participant_id,updated_at) VALUES('contact',0)`); err != nil {
		t.Fatal(err)
	}
	observe(t, s, "starting", "data")
	if mismatch, err := s.ObserveSnapshot(ctx, "contacts", 0); err != nil || !mismatch {
		t.Fatalf("mismatch=%v err=%v", mismatch, err)
	}
	if _, err := s.ObserveSnapshot(ctx, "conversations", 1); err != nil {
		t.Fatal(err)
	}
	observe(t, s, "phone", "heartbeat", "data")
	status(t, s, "unknown")
	if _, err := s.ObserveSnapshot(ctx, "contacts", 1); err != nil {
		t.Fatal(err)
	}
	status(t, s, "recently_verified")
}
