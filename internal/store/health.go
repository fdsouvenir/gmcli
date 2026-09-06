package store

import (
	"context"
	"fmt"
	"time"
)

// Health contains observations, not a claim that an offline doctor has probed the phone.
// Millisecond timestamps are receipt times; message dates are deliberately excluded.
type Health struct {
	ProcessHeartbeat           int64  `json:"process_heartbeat_ms"`
	Transport                  string `json:"transport"`
	TransportObserved          int64  `json:"transport_observed_ms"`
	ProtocolEvidence           int64  `json:"protocol_evidence_ms"`
	DataEvidence               int64  `json:"data_evidence_ms"`
	PhoneResponse              int64  `json:"phone_response_ms"`
	Phone                      string `json:"phone"`
	SnapshotVerified           int64  `json:"snapshot_verified_ms"`
	ArchiveState               string `json:"archive_state"`
	ContactsSnapshotEmpty      bool   `json:"contacts_snapshot_empty"`
	ConversationsSnapshotEmpty bool   `json:"conversations_snapshot_empty"`
	Invalidation               string `json:"invalidation,omitempty"`
}

// ObserveHealth atomically records a narrowly defined observation. Heartbeats never
// refresh network evidence; failures remain latched until a new connection attempt.
func (s *Store) ObserveHealth(ctx context.Context, kind string) error {
	now := time.Now().UnixMilli()
	var set string
	switch kind {
	case "starting":
		set = `process_heartbeat=?, transport='connecting', transport_observed=0, protocol_evidence=0, data_evidence=0, phone_response=0, phone='unknown', invalidation='', snapshot_verified=0, archive_state='unknown', contacts_snapshot_empty=0, conversations_snapshot_empty=0`
	case "heartbeat":
		set = `process_heartbeat=?`
	case "transport_requested":
		set = `transport=CASE WHEN transport='connecting' THEN 'requested' ELSE transport END`
	case "transport":
		set = `transport_observed=?, transport='observed'`
	case "protocol":
		set = `protocol_evidence=?`
	case "data":
		set = `data_evidence=?, protocol_evidence=?, phone_response=?, transport='observed', transport_observed=?, phone='responding', archive_state='observed'`
	case "phone":
		set = `phone_response=?, protocol_evidence=?, transport='observed', transport_observed=?, phone='responding'`
	case "phone_unresponsive":
		set = `phone='unresponsive', phone_response=0, data_evidence=0, snapshot_verified=0`
	case "temporary_error":
		set = `transport_observed=?, transport='interrupted', phone='unknown', phone_response=0, protocol_evidence=0, data_evidence=0, snapshot_verified=0`
	case "no_data", "snapshot_invalid":
		set = `data_evidence=0, snapshot_verified=0, archive_state='unverified'`
	case "auth_invalid", "fatal":
		set = fmt.Sprintf(`transport_observed=?, transport='disconnected', phone='unknown', invalidation='%s'`, kind)
	case "disconnected", "session_changed":
		set = fmt.Sprintf(`transport_observed=?, transport='disconnected', phone='unknown', invalidation=CASE WHEN invalidation='' THEN '%s' ELSE invalidation END`, kind)
	default:
		return fmt.Errorf("unknown health observation %q", kind)
	}
	args := []any{now}
	if kind == "phone_unresponsive" || kind == "transport_requested" || kind == "no_data" || kind == "snapshot_invalid" {
		args = nil
	}
	if kind == "data" {
		args = append(args, now, now, now)
	}
	if kind == "phone" {
		args = append(args, now, now)
	}
	where := " WHERE id=1"
	if kind != "starting" && kind != "auth_invalid" && kind != "fatal" && kind != "disconnected" && kind != "session_changed" {
		where += " AND invalidation=''"
	}
	_, err := s.db.ExecContext(ctx, "UPDATE connection_health SET "+set+where, args...)
	return err
}

func (s *Store) Health(ctx context.Context) (Health, error) {
	var h Health
	err := s.db.QueryRowContext(ctx, `SELECT process_heartbeat, transport, transport_observed, protocol_evidence, data_evidence, phone_response, phone, invalidation, snapshot_verified, archive_state, contacts_snapshot_empty, conversations_snapshot_empty FROM connection_health WHERE id=1`).Scan(&h.ProcessHeartbeat, &h.Transport, &h.TransportObserved, &h.ProtocolEvidence, &h.DataEvidence, &h.PhoneResponse, &h.Phone, &h.Invalidation, &h.SnapshotVerified, &h.ArchiveState, &h.ContactsSnapshotEmpty, &h.ConversationsSnapshotEmpty)
	return h, err
}

// Assessment is bounded evidence, never an assertion of current connectivity.
func (h Health) Assessment(now time.Time) (string, string) {
	fresh := func(ms int64) bool {
		age := now.Sub(time.UnixMilli(ms))
		return ms > 0 && age >= 0 && age <= 15*time.Minute
	}
	if h.Invalidation != "" {
		return "unhealthy", "connection invalidated: " + h.Invalidation
	}
	if h.ContactsSnapshotEmpty || h.ConversationsSnapshotEmpty {
		return "unknown", "empty initial snapshot against populated archive; a successful corrected snapshot is required"
	}
	if h.ArchiveState == "unverified" {
		return "unknown", "archive data missing; actual data or a validated snapshot is required"
	}
	if h.Phone == "unresponsive" || h.Transport == "interrupted" {
		return "unhealthy", "phone or transport is not responding"
	}
	if h.ProcessHeartbeat == 0 {
		return "unknown", "no evidence-aware sync run; legacy activity may be synthetic and does not prove connectivity"
	}
	if !fresh(h.ProcessHeartbeat) {
		return "unknown", "sync process heartbeat is stale"
	}
	if h.Phone != "responding" || h.Transport != "observed" || !fresh(h.ProtocolEvidence) || !fresh(h.PhoneResponse) || (!fresh(h.DataEvidence) && !fresh(h.SnapshotVerified)) {
		return "unknown", "no recent meaningful archive data confirmation and phone/protocol response; heartbeat or phone ping alone is insufficient"
	}
	return "recently_verified", ""
}

// ObserveSnapshot records a successful, non-nil, structurally valid list response.
// Only conversation snapshots verify the archive; contacts and phone pings cannot.
func (s *Store) ObserveSnapshot(ctx context.Context, kind string, validRows int) (bool, error) {
	var archived int
	var err error
	column := ""
	switch kind {
	case "contacts":
		archived, err = s.CountContacts(ctx)
		column = "contacts_snapshot_empty"
	case "conversations":
		archived, err = s.CountConversations(ctx)
		column = "conversations_snapshot_empty"
		if err == nil && archived == 0 {
			archived, err = s.CountMessages(ctx)
		}
	default:
		return false, fmt.Errorf("unknown snapshot kind %q", kind)
	}
	if err != nil {
		return false, err
	}
	if validRows < 0 {
		return false, fmt.Errorf("negative snapshot count")
	}
	mismatch := validRows == 0 && archived > 0
	if err = s.ObserveHealth(ctx, "phone"); err != nil {
		return false, err
	}
	set := column + "=?"
	args := []any{mismatch}
	if kind == "conversations" {
		if !mismatch {
			set += ", snapshot_verified=?, archive_state='observed'"
			args = append(args, time.Now().UnixMilli())
		} else {
			set += ", snapshot_verified=0"
		}
	}
	_, err = s.db.ExecContext(ctx, "UPDATE connection_health SET "+set+" WHERE id=1 AND invalidation=''", args...)
	return mismatch, err
}
