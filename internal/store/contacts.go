package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Contact is the storage shape for an address-book entry. ParticipantID is
// the libgm-stable ID and forms the primary key; ContactID is Google's
// contact-database ID (may be empty for non-saved numbers).
//
// Alias is the local user label (from the aliases table) when one is set.
// DisplayName is Alias if non-empty, otherwise Name — render code should
// always read DisplayName, never Name directly.
type Contact struct {
	ParticipantID   string `json:"participant_id"`
	SourcePlatform  string `json:"source_platform"`
	ContactID       string `json:"contact_id"`
	Name            string `json:"name"`
	E164            string `json:"e164"`
	FormattedNumber string `json:"formatted_number"`
	AvatarColor     string `json:"avatar_color"`
	IsMe            bool   `json:"is_me"`
	Alias           string `json:"alias,omitempty"`
	DisplayName     string `json:"display_name,omitempty"`
}

// UpsertContact inserts or updates a contact row by ParticipantID.
func (s *Store) UpsertContact(ctx context.Context, c Contact) error {
	if c.ParticipantID == "" {
		return fmt.Errorf("participant id is required")
	}
	platform := c.SourcePlatform
	if platform == "" {
		platform = "gm"
	}
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO contacts (
			participant_id, source_platform, contact_id, name, e164,
			formatted_number, avatar_color, is_me, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(participant_id) DO UPDATE SET
			source_platform  = excluded.source_platform,
			contact_id       = excluded.contact_id,
			name             = excluded.name,
			e164             = excluded.e164,
			formatted_number = excluded.formatted_number,
			avatar_color     = excluded.avatar_color,
			is_me            = excluded.is_me,
			updated_at       = excluded.updated_at
	`,
		c.ParticipantID, platform, c.ContactID, c.Name, c.E164,
		c.FormattedNumber, c.AvatarColor, boolToInt(c.IsMe), now,
	)
	if err != nil {
		return fmt.Errorf("upsert contact %s: %w", c.ParticipantID, err)
	}
	return nil
}

// CountContacts returns the total number of stored contacts.
func (s *Store) CountContacts(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contacts`).Scan(&n)
	return n, err
}

// SearchContacts returns contacts matching query against name, alias, e164,
// or formatted_number using a case-insensitive substring match. Limit <=0
// means 50.
func (s *Store) SearchContacts(ctx context.Context, query string, limit int) ([]Contact, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `
		SELECT c.participant_id, c.source_platform, c.contact_id, c.name, c.e164,
		       c.formatted_number, c.avatar_color, c.is_me,
		       COALESCE(a.alias, '') AS alias
		  FROM contacts c
		  LEFT JOIN aliases a
		    ON a.target_type = 'contact' AND a.target_id = c.participant_id`
	var args []any
	if query != "" {
		// Match against name, alias, e164, formatted_number. SQLite LIKE is
		// case-insensitive for ASCII by default; for non-ASCII names we'd
		// need an ICU-aware collation, which modernc.org/sqlite doesn't
		// ship — acceptable for the scaffold.
		like := "%" + strings.ReplaceAll(strings.ReplaceAll(query, `\`, `\\`), `%`, `\%`) + "%"
		q += `
		 WHERE c.name LIKE ? ESCAPE '\'
		    OR a.alias LIKE ? ESCAPE '\'
		    OR c.e164 LIKE ? ESCAPE '\'
		    OR c.formatted_number LIKE ? ESCAPE '\'`
		args = append(args, like, like, like, like)
	}
	q += " ORDER BY COALESCE(a.alias, c.name) COLLATE NOCASE ASC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("search contacts: %w", err)
	}
	defer rows.Close()
	out := make([]Contact, 0)
	for rows.Next() {
		c, err := scanContactWithAlias(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetContact looks up a contact by participant_id (exact). For phone-number
// or contact_id lookup, see GetContactByNumber. Returns ErrNotFound on miss.
func (s *Store) GetContact(ctx context.Context, participantID string) (Contact, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT c.participant_id, c.source_platform, c.contact_id, c.name, c.e164,
		       c.formatted_number, c.avatar_color, c.is_me,
		       COALESCE(a.alias, '') AS alias
		  FROM contacts c
		  LEFT JOIN aliases a
		    ON a.target_type = 'contact' AND a.target_id = c.participant_id
		 WHERE c.participant_id = ?`, participantID)
	c, err := scanContactWithAlias(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Contact{}, ErrNotFound
	}
	return c, err
}

// GetContactByNumber finds the first contact whose e164 or formatted_number
// matches the given query exactly. Used by `gmcli contacts show` to accept
// either a participant_id or a phone number.
func (s *Store) GetContactByNumber(ctx context.Context, number string) (Contact, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT c.participant_id, c.source_platform, c.contact_id, c.name, c.e164,
		       c.formatted_number, c.avatar_color, c.is_me,
		       COALESCE(a.alias, '') AS alias
		  FROM contacts c
		  LEFT JOIN aliases a
		    ON a.target_type = 'contact' AND a.target_id = c.participant_id
		 WHERE c.e164 = ? OR c.formatted_number = ?
		 LIMIT 1`, number, number)
	c, err := scanContactWithAlias(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Contact{}, ErrNotFound
	}
	return c, err
}

func scanContactWithAlias(r interface {
	Scan(...any) error
}) (Contact, error) {
	var c Contact
	var isMe int64
	if err := r.Scan(
		&c.ParticipantID, &c.SourcePlatform, &c.ContactID, &c.Name, &c.E164,
		&c.FormattedNumber, &c.AvatarColor, &isMe, &c.Alias,
	); err != nil {
		return Contact{}, err
	}
	c.IsMe = isMe != 0
	c.DisplayName = c.Name
	if c.Alias != "" {
		c.DisplayName = c.Alias
	}
	return c, nil
}
