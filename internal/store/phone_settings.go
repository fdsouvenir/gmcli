package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PhoneSettings is the cached send-readiness metadata from gmproto.Settings.
type PhoneSettings struct {
	RawProto  []byte
	SIMCount  int
	UpdatedAt time.Time
}

// SavePhoneSettings stores the latest phone settings event. rawProto must be
// the marshaled gmproto.Settings bytes; store keeps it opaque.
func (s *Store) SavePhoneSettings(ctx context.Context, rawProto []byte, simCount int) error {
	if len(rawProto) == 0 {
		return fmt.Errorf("phone settings raw proto is required")
	}
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO phone_settings (id, raw_proto, sim_count, updated_at)
		VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			raw_proto  = excluded.raw_proto,
			sim_count  = excluded.sim_count,
			updated_at = excluded.updated_at
	`, rawProto, simCount, now)
	if err != nil {
		return fmt.Errorf("save phone settings: %w", err)
	}
	return nil
}

// LatestPhoneSettings returns the most recent cached phone settings.
func (s *Store) LatestPhoneSettings(ctx context.Context) (PhoneSettings, error) {
	var ps PhoneSettings
	var updated int64
	err := s.db.QueryRowContext(ctx, `
		SELECT raw_proto, sim_count, updated_at
		  FROM phone_settings
		 WHERE id = 1
	`).Scan(&ps.RawProto, &ps.SIMCount, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return PhoneSettings{}, ErrNotFound
	}
	if err != nil {
		return PhoneSettings{}, err
	}
	ps.UpdatedAt = time.UnixMilli(updated)
	return ps, nil
}
