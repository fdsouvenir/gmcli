package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AliasTarget enumerates the things that can carry a local alias.
type AliasTarget string

const (
	AliasContact      AliasTarget = "contact"
	AliasConversation AliasTarget = "conversation"
)

// Alias is a user-set local label overriding the libgm-supplied name.
type Alias struct {
	TargetType AliasTarget `json:"target_type"`
	TargetID   string      `json:"target_id"`
	Alias      string      `json:"alias"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// SetAlias upserts a local alias. Empty alias is rejected — use RemoveAlias
// to delete.
func (s *Store) SetAlias(ctx context.Context, target AliasTarget, id, alias string) error {
	id = strings.TrimSpace(id)
	alias = strings.TrimSpace(alias)
	if id == "" {
		return fmt.Errorf("target id is required")
	}
	if alias == "" {
		return fmt.Errorf("alias is required (use RemoveAlias to delete)")
	}
	if target != AliasContact && target != AliasConversation {
		return fmt.Errorf("unknown alias target %q", target)
	}
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO aliases (target_type, target_id, alias, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(target_type, target_id) DO UPDATE SET
			alias      = excluded.alias,
			updated_at = excluded.updated_at
	`, string(target), id, alias, now)
	if err != nil {
		return fmt.Errorf("set alias: %w", err)
	}
	return nil
}

// RemoveAlias deletes an alias. Returns ErrNotFound if no alias was set.
func (s *Store) RemoveAlias(ctx context.Context, target AliasTarget, id string) error {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM aliases WHERE target_type = ? AND target_id = ?
	`, string(target), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetAlias returns the alias for a target, or ErrNotFound.
func (s *Store) GetAlias(ctx context.Context, target AliasTarget, id string) (string, error) {
	var a string
	err := s.db.QueryRowContext(ctx, `
		SELECT alias FROM aliases WHERE target_type = ? AND target_id = ?
	`, string(target), id).Scan(&a)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return a, err
}

// ListAliases returns all aliases ordered by target_type, target_id.
func (s *Store) ListAliases(ctx context.Context) ([]Alias, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT target_type, target_id, alias, updated_at
		  FROM aliases
		 ORDER BY target_type, alias COLLATE NOCASE
	`)
	if err != nil {
		return nil, fmt.Errorf("list aliases: %w", err)
	}
	defer rows.Close()
	var out []Alias
	for rows.Next() {
		var a Alias
		var typ string
		var ts int64
		if err := rows.Scan(&typ, &a.TargetID, &a.Alias, &ts); err != nil {
			return nil, err
		}
		a.TargetType = AliasTarget(typ)
		a.UpdatedAt = time.UnixMilli(ts)
		out = append(out, a)
	}
	return out, rows.Err()
}

// DisplayName resolves the user-facing label for a contact: alias if one is
// set, otherwise the contact's libgm-supplied name. Used by render code so
// aliases show up uniformly across all CLI surfaces.
func (s *Store) DisplayName(ctx context.Context, target AliasTarget, id, fallback string) string {
	a, err := s.GetAlias(ctx, target, id)
	if err == nil && a != "" {
		return a
	}
	return fallback
}
