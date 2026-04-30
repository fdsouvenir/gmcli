package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Conversation is the storage shape for a chat thread. participants_json
// holds the libgm Participant array as JSON to avoid a join table at this
// stage — Phase 3 may normalize if query patterns demand it.
type Conversation struct {
	ID                string    `json:"conversation_id"`
	SourcePlatform    string    `json:"source_platform"`
	Name              string    `json:"name"`
	IsGroup           bool      `json:"is_group"`
	ParticipantsJSON  string    `json:"participants_json"`
	LastMessageTimeMS int64     `json:"last_message_time_ms"`
	Unread            bool      `json:"unread"`
	Pinned            bool      `json:"pinned"`
	Archived          bool      `json:"archived"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// UpsertConversation inserts or updates a conversation row by ID.
func (s *Store) UpsertConversation(ctx context.Context, c Conversation) error {
	if c.ID == "" {
		return fmt.Errorf("conversation id is required")
	}
	platform := c.SourcePlatform
	if platform == "" {
		platform = "gm"
	}
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO conversations (
			conversation_id, source_platform, name, is_group, participants_json,
			last_message_ts, unread, pinned, archived, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(conversation_id) DO UPDATE SET
			source_platform   = excluded.source_platform,
			name              = excluded.name,
			is_group          = excluded.is_group,
			participants_json = excluded.participants_json,
			last_message_ts   = MAX(conversations.last_message_ts, excluded.last_message_ts),
			unread            = excluded.unread,
			pinned            = excluded.pinned,
			archived          = excluded.archived,
			updated_at        = excluded.updated_at
	`,
		c.ID, platform, c.Name, boolToInt(c.IsGroup), nullableJSON(c.ParticipantsJSON),
		c.LastMessageTimeMS, boolToInt(c.Unread), boolToInt(c.Pinned), boolToInt(c.Archived), now,
	)
	if err != nil {
		return fmt.Errorf("upsert conversation %s: %w", c.ID, err)
	}
	return nil
}

// GetConversation fetches a single row. Returns sql.ErrNoRows on miss.
func (s *Store) GetConversation(ctx context.Context, id string) (Conversation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT conversation_id, source_platform, name, is_group, participants_json,
		       last_message_ts, unread, pinned, archived, updated_at
		  FROM conversations
		 WHERE conversation_id = ?
	`, id)
	return scanConversation(row)
}

// CountConversations returns the total number of stored conversations.
func (s *Store) CountConversations(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversations`).Scan(&n)
	return n, err
}

// ListConversationOpts filters and paginates ListConversations.
type ListConversationOpts struct {
	Limit      int  // max rows; <=0 means 50
	UnreadOnly bool // only conversations with unread=1
	Pinned     bool // only pinned threads
}

// ListConversations returns conversations ordered by last_message_ts DESC.
func (s *Store) ListConversations(ctx context.Context, opts ListConversationOpts) ([]Conversation, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	q := `
		SELECT conversation_id, source_platform, name, is_group, participants_json,
		       last_message_ts, unread, pinned, archived, updated_at
		  FROM conversations`
	var clauses []string
	if opts.UnreadOnly {
		clauses = append(clauses, "unread = 1")
	}
	if opts.Pinned {
		clauses = append(clauses, "pinned = 1")
	}
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	q += " ORDER BY last_message_ts DESC, updated_at DESC LIMIT ?"
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	out := make([]Conversation, 0)
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// scanConversation reads a single row in the canonical column order. Used by
// both GetConversation and ListConversations.
func scanConversation(r interface {
	Scan(...any) error
}) (Conversation, error) {
	var c Conversation
	var lastMsg, updated, isGroup, unread, pinned, archived int64
	if err := r.Scan(
		&c.ID, &c.SourcePlatform, &c.Name, &isGroup, &c.ParticipantsJSON,
		&lastMsg, &unread, &pinned, &archived, &updated,
	); err != nil {
		return Conversation{}, err
	}
	c.IsGroup = isGroup != 0
	c.Unread = unread != 0
	c.Pinned = pinned != 0
	c.Archived = archived != 0
	c.LastMessageTimeMS = lastMsg
	c.UpdatedAt = time.UnixMilli(updated)
	return c, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullableJSON(s string) any {
	if s == "" {
		return "[]"
	}
	return s
}
