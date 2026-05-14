// Package store is gmcli's local SQLite + FTS5 archive of conversations,
// messages, and contacts. It is intentionally narrow — domain code converts
// libgm protos into store models at the boundary and uses these helpers for
// upserts and queries.
//
// All upserts use INSERT ... ON CONFLICT DO UPDATE (sqlite UPSERT) so the
// sync loop can replay events without dedup logic of its own.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, FTS5 enabled in default build
)

// Store wraps *sql.DB with gmcli-specific helpers. Construct with Open.
type Store struct {
	db *sql.DB
}

// Open returns a Store backed by the SQLite file at path, applying any
// pending migrations. The caller owns Close.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := buildDSN(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // SQLite is single-writer; serialize via the pool.
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// DB exposes the underlying handle. Useful for tests and ad-hoc queries; the
// rest of the package should prefer typed helpers.
func (s *Store) DB() *sql.DB { return s.db }

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	current, err := s.currentVersion(ctx)
	if err != nil {
		return err
	}
	for i, mig := range migrations {
		v := i + 1
		if v <= current {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration v%d: %w", v, err)
		}
		if _, err := tx.ExecContext(ctx, mig); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration v%d: %w", v, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration v%d: %w", v, err)
		}
	}
	return nil
}

func (s *Store) currentVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_version`).Scan(&v)
	if err == nil {
		return int(v.Int64), nil
	}
	// On a fresh database, schema_version doesn't exist yet. modernc.org/sqlite
	// surfaces this as a string error; match it explicitly.
	if strings.Contains(err.Error(), "no such table") {
		return 0, nil
	}
	return 0, err
}

// SchemaVersion returns the highest applied schema migration version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	return s.currentVersion(ctx)
}

// MarkSync updates the sync_state row. Called by the sync loop on each
// successful event delivery so doctor can surface a "last seen" timestamp.
func (s *Store) MarkSync(ctx context.Context, lastEventTime, connectTime time.Time) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		UPDATE sync_state
		   SET last_event_ts = MAX(last_event_ts, ?),
		       last_connect_ts = MAX(last_connect_ts, ?),
		       updated_at = ?
		 WHERE id = 1`,
		lastEventTime.UnixMilli(),
		connectTime.UnixMilli(),
		now,
	)
	return err
}

// TouchSync records that the sync loop is still active without changing the
// latest archived message or connection timestamp.
func (s *Store) TouchSync(ctx context.Context) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		UPDATE sync_state
		   SET updated_at = MAX(updated_at, ?)
		 WHERE id = 1`,
		now,
	)
	return err
}

// SyncState is what doctor/--json emit about freshness.
type SyncState struct {
	LastEventTime   time.Time
	LastConnectTime time.Time
	UpdatedAt       time.Time
}

// SyncState returns the freshness row.
func (s *Store) SyncState(ctx context.Context) (SyncState, error) {
	var lastEvent, lastConnect, updated int64
	err := s.db.QueryRowContext(ctx, `
		SELECT last_event_ts, last_connect_ts, updated_at
		  FROM sync_state WHERE id = 1`,
	).Scan(&lastEvent, &lastConnect, &updated)
	if err != nil {
		return SyncState{}, err
	}
	return SyncState{
		LastEventTime:   time.UnixMilli(lastEvent),
		LastConnectTime: time.UnixMilli(lastConnect),
		UpdatedAt:       time.UnixMilli(updated),
	}, nil
}

// buildDSN constructs a SQLite DSN with WAL, foreign keys, and a reasonable
// busy timeout. modernc.org/sqlite uses the "_pragma=" query parameter to
// inline pragmas; multiple are passed as repeated values.
func buildDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "busy_timeout(5000)")
	return "file:" + path + "?" + q.Encode()
}
