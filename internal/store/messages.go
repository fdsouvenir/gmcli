package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a row lookup misses. Callers can use
// errors.Is to distinguish it from real I/O errors.
var ErrNotFound = errors.New("not found")

// Message is the storage shape for a single message. Body is the plaintext
// content if any (nil for media-only). MediaID/MimeType/DecryptionKey
// describe the attachment if present; the bytes themselves are only fetched
// by the explicit `media download` command.
type Message struct {
	ID             string  `json:"message_id"`
	ConversationID string  `json:"conversation_id"`
	SourcePlatform string  `json:"source_platform"`
	SenderID       string  `json:"sender_id"`
	Body           *string `json:"body,omitempty"`
	TimestampMS    int64   `json:"timestamp_ms"`
	Status         int64   `json:"status"`
	IsFromMe       bool    `json:"is_from_me"`
	MediaID        *string `json:"media_id,omitempty"`
	MimeType       *string `json:"mime_type,omitempty"`
	DecryptionKey  []byte  `json:"-"`
	ReactionsJSON  *string `json:"reactions_json,omitempty"`
	ReplyToID      *string `json:"reply_to_id,omitempty"`
	RawProto       []byte  `json:"-"`
}

// UpsertMessage inserts or updates a message row by ID.
func (s *Store) UpsertMessage(ctx context.Context, m Message) error {
	if m.ID == "" {
		return fmt.Errorf("message id is required")
	}
	if m.ConversationID == "" {
		return fmt.Errorf("conversation id is required")
	}
	platform := m.SourcePlatform
	if platform == "" {
		platform = "gm"
	}
	now := time.Now().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO conversations (
			conversation_id, source_platform, updated_at
		) VALUES (?, ?, ?)
	`, m.ConversationID, platform, now); err != nil {
		return fmt.Errorf("ensure conversation %s: %w", m.ConversationID, err)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (
			message_id, conversation_id, source_platform, sender_id,
			body, timestamp_ms, status, is_from_me,
			media_id, mime_type, decryption_key,
			reactions_json, reply_to_id, raw_proto, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id) DO UPDATE SET
			conversation_id = excluded.conversation_id,
			source_platform = excluded.source_platform,
			sender_id       = excluded.sender_id,
			body            = excluded.body,
			timestamp_ms    = excluded.timestamp_ms,
			status          = excluded.status,
			is_from_me      = excluded.is_from_me,
			media_id        = excluded.media_id,
			mime_type       = excluded.mime_type,
			decryption_key  = excluded.decryption_key,
			reactions_json  = excluded.reactions_json,
			reply_to_id     = excluded.reply_to_id,
			raw_proto       = excluded.raw_proto,
			updated_at      = excluded.updated_at
	`,
		m.ID, m.ConversationID, platform, m.SenderID,
		m.Body, m.TimestampMS, m.Status, boolToInt(m.IsFromMe),
		m.MediaID, m.MimeType, nullBytes(m.DecryptionKey),
		m.ReactionsJSON, m.ReplyToID, nullBytes(m.RawProto), now,
	)
	if err != nil {
		return fmt.Errorf("upsert message %s: %w", m.ID, err)
	}
	return nil
}

// CountMessages returns the total number of stored messages.
func (s *Store) CountMessages(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&n)
	return n, err
}

// ListMessageOpts describes a message-list query. Times are inclusive.
type ListMessageOpts struct {
	ConversationID string    // optional; if empty, all conversations
	SenderID       string    // optional participant_id filter
	Since          time.Time // optional lower bound
	Until          time.Time // optional upper bound
	Limit          int       // <=0 means 50
	Order          string    // "asc" or "desc" (default "desc")
}

// ListMessages returns messages matching opts, ordered by timestamp.
func (s *Store) ListMessages(ctx context.Context, opts ListMessageOpts) ([]Message, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	q := strings.Builder{}
	q.WriteString(`
		SELECT message_id, conversation_id, source_platform, sender_id, body,
		       timestamp_ms, status, is_from_me, media_id, mime_type,
		       decryption_key, reactions_json, reply_to_id
		  FROM messages`)
	var (
		args    []any
		clauses []string
	)
	if opts.ConversationID != "" {
		clauses = append(clauses, "conversation_id = ?")
		args = append(args, opts.ConversationID)
	}
	if opts.SenderID != "" {
		clauses = append(clauses, "sender_id = ?")
		args = append(args, opts.SenderID)
	}
	if !opts.Since.IsZero() {
		clauses = append(clauses, "timestamp_ms >= ?")
		args = append(args, opts.Since.UnixMilli())
	}
	if !opts.Until.IsZero() {
		clauses = append(clauses, "timestamp_ms <= ?")
		args = append(args, opts.Until.UnixMilli())
	}
	if len(clauses) > 0 {
		q.WriteString(" WHERE " + strings.Join(clauses, " AND "))
	}
	order := strings.ToUpper(opts.Order)
	if order != "ASC" {
		order = "DESC"
	}
	fmt.Fprintf(&q, " ORDER BY timestamp_ms %s LIMIT ?", order)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()
	out := make([]Message, 0)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMessage fetches a single message by id. Returns ErrNotFound on miss.
func (s *Store) GetMessage(ctx context.Context, id string) (Message, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT message_id, conversation_id, source_platform, sender_id, body,
		       timestamp_ms, status, is_from_me, media_id, mime_type,
		       decryption_key, reactions_json, reply_to_id
		  FROM messages
		 WHERE message_id = ?`, id)
	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	return m, err
}

// GetMessageContext returns up to `before` messages preceding the anchor and
// up to `after` messages following it, all in the same conversation. The
// anchor itself is always included. Result is ordered by timestamp ASC.
func (s *Store) GetMessageContext(ctx context.Context, anchorID string, before, after int) ([]Message, error) {
	if before < 0 {
		before = 0
	}
	if after < 0 {
		after = 0
	}
	anchor, err := s.GetMessage(ctx, anchorID)
	if err != nil {
		return nil, err
	}
	out := []Message{anchor}
	if before > 0 {
		rows, err := s.db.QueryContext(ctx, `
			SELECT message_id, conversation_id, source_platform, sender_id, body,
			       timestamp_ms, status, is_from_me, media_id, mime_type,
			       decryption_key, reactions_json, reply_to_id
			  FROM messages
			 WHERE conversation_id = ?
			   AND (timestamp_ms < ? OR (timestamp_ms = ? AND message_id < ?))
			 ORDER BY timestamp_ms DESC, message_id DESC
			 LIMIT ?`,
			anchor.ConversationID, anchor.TimestampMS, anchor.TimestampMS, anchor.ID, before)
		if err != nil {
			return nil, err
		}
		var pre []Message
		for rows.Next() {
			m, err := scanMessage(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			pre = append(pre, m)
		}
		rows.Close()
		// pre is in DESC order; flip to ASC and prepend.
		for i, j := 0, len(pre)-1; i < j; i, j = i+1, j-1 {
			pre[i], pre[j] = pre[j], pre[i]
		}
		out = append(pre, out...)
	}
	if after > 0 {
		rows, err := s.db.QueryContext(ctx, `
			SELECT message_id, conversation_id, source_platform, sender_id, body,
			       timestamp_ms, status, is_from_me, media_id, mime_type,
			       decryption_key, reactions_json, reply_to_id
			  FROM messages
			 WHERE conversation_id = ?
			   AND (timestamp_ms > ? OR (timestamp_ms = ? AND message_id > ?))
			 ORDER BY timestamp_ms ASC, message_id ASC
			 LIMIT ?`,
			anchor.ConversationID, anchor.TimestampMS, anchor.TimestampMS, anchor.ID, after)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			m, err := scanMessage(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, m)
		}
		rows.Close()
	}
	return out, nil
}

func scanMessage(r interface {
	Scan(...any) error
}) (Message, error) {
	var m Message
	var fromMe int64
	var body, mediaID, mimeType, reactions, replyTo sql.NullString
	var dec []byte
	if err := r.Scan(
		&m.ID, &m.ConversationID, &m.SourcePlatform, &m.SenderID, &body,
		&m.TimestampMS, &m.Status, &fromMe, &mediaID, &mimeType,
		&dec, &reactions, &replyTo,
	); err != nil {
		return Message{}, err
	}
	m.IsFromMe = fromMe != 0
	if body.Valid {
		s := body.String
		m.Body = &s
	}
	if mediaID.Valid {
		s := mediaID.String
		m.MediaID = &s
	}
	if mimeType.Valid {
		s := mimeType.String
		m.MimeType = &s
	}
	if reactions.Valid {
		s := reactions.String
		m.ReactionsJSON = &s
	}
	if replyTo.Valid {
		s := replyTo.String
		m.ReplyToID = &s
	}
	if len(dec) > 0 {
		m.DecryptionKey = dec
	}
	return m, nil
}

// SearchHit is one FTS result. Snippet is the FTS5-generated highlighted
// excerpt around the match, suitable for display in --json output.
type SearchHit struct {
	MessageID      string `json:"message_id"`
	ConversationID string `json:"conversation_id"`
	Body           string `json:"body"`
	Snippet        string `json:"snippet"`
	TimestampMS    int64  `json:"timestamp_ms"`
	IsFromMe       bool   `json:"is_from_me"`
}

// SearchMessages runs an FTS5 MATCH against messages_fts. limit caps the
// result count. The query string is passed to FTS5 verbatim, so callers can
// use the standard syntax (phrase quotes, NEAR(), AND/OR/NOT).
func (s *Store) SearchMessages(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.message_id, m.conversation_id,
		       COALESCE(m.body, ''),
		       snippet(messages_fts, 1, '[', ']', ' … ', 12),
		       m.timestamp_ms, m.is_from_me
		  FROM messages_fts
		  JOIN messages m ON m.message_id = messages_fts.message_id
		 WHERE messages_fts MATCH ?
		 ORDER BY m.timestamp_ms DESC
		 LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("fts search %q: %w", query, err)
	}
	defer rows.Close()
	out := make([]SearchHit, 0)
	for rows.Next() {
		var h SearchHit
		var fromMe int64
		if err := rows.Scan(&h.MessageID, &h.ConversationID, &h.Body, &h.Snippet, &h.TimestampMS, &fromMe); err != nil {
			return nil, fmt.Errorf("scan fts row: %w", err)
		}
		h.IsFromMe = fromMe != 0
		out = append(out, h)
	}
	return out, rows.Err()
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
