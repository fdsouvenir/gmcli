// Package archive exports portable snapshots of gmcli's local store.
package archive

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fdsouvenir/gmcli/internal/store"
)

const FormatVersion = 1

// Result describes a completed JSON archive export.
type Result struct {
	Path          string `json:"path"`
	Format        string `json:"format"`
	FormatVersion int    `json:"format_version"`
	SchemaVersion int    `json:"schema_version"`
	Conversations int    `json:"conversations"`
	Messages      int    `json:"messages"`
	Contacts      int    `json:"contacts"`
	Aliases       int    `json:"aliases"`
}

type conversation struct {
	ID                string    `json:"conversation_id"`
	SourcePlatform    string    `json:"source_platform"`
	Name              string    `json:"name"`
	IsGroup           bool      `json:"is_group"`
	Participants      jsonValue `json:"participants"`
	LastMessageTimeMS int64     `json:"last_message_time_ms"`
	Unread            bool      `json:"unread"`
	Pinned            bool      `json:"pinned"`
	Archived          bool      `json:"archived"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type message struct {
	ID             string     `json:"message_id"`
	ConversationID string     `json:"conversation_id"`
	SourcePlatform string     `json:"source_platform"`
	SenderID       string     `json:"sender_id"`
	Body           *string    `json:"body,omitempty"`
	TimestampMS    int64      `json:"timestamp_ms"`
	Status         int64      `json:"status"`
	IsFromMe       bool       `json:"is_from_me"`
	MediaID        *string    `json:"media_id,omitempty"`
	MimeType       *string    `json:"mime_type,omitempty"`
	Reactions      *jsonValue `json:"reactions,omitempty"`
	ReplyToID      *string    `json:"reply_to_id,omitempty"`
}

// WriteJSON writes a consistent, portable snapshot to path. The destination
// is replaced only after the full JSON document has been written successfully.
func WriteJSON(ctx context.Context, st *store.Store, path string, force bool) (Result, error) {
	if path == "" {
		return Result{}, fmt.Errorf("output path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Result{}, fmt.Errorf("resolve output path: %w", err)
	}
	if !force {
		if _, err := os.Stat(abs); err == nil {
			return Result{}, fmt.Errorf("output already exists: %s (use --force to replace it)", abs)
		} else if !os.IsNotExist(err) {
			return Result{}, fmt.Errorf("inspect output %s: %w", abs, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return Result{}, fmt.Errorf("create output directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(abs), ".gmcli-export-*.json")
	if err != nil {
		return Result{}, fmt.Errorf("create temporary export: %w", err)
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return Result{}, fmt.Errorf("secure temporary export: %w", err)
	}

	result, err := writeSnapshot(ctx, st, tmp)
	if err != nil {
		return Result{}, err
	}
	if err := tmp.Sync(); err != nil {
		return Result{}, fmt.Errorf("sync export: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Result{}, fmt.Errorf("close export: %w", err)
	}
	if err := os.Rename(tmpPath, abs); err != nil {
		return Result{}, fmt.Errorf("install export: %w", err)
	}
	keep = true
	result.Path = abs
	return result, nil
}

func writeSnapshot(ctx context.Context, st *store.Store, dst io.Writer) (Result, error) {
	tx, err := st.DB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Result{}, fmt.Errorf("begin archive snapshot: %w", err)
	}
	defer tx.Rollback()

	var schemaVersion int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&schemaVersion); err != nil {
		return Result{}, fmt.Errorf("read schema version: %w", err)
	}

	result := Result{Format: "gmcli-json-archive", FormatVersion: FormatVersion, SchemaVersion: schemaVersion}
	w := bufio.NewWriter(dst)
	if _, err := fmt.Fprintf(w, "{\n  \"format\": %q,\n  \"format_version\": %d,\n  \"schema_version\": %d,\n  \"exported_at\": %q", result.Format, result.FormatVersion, schemaVersion, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return Result{}, err
	}

	if result.Conversations, err = writeConversations(ctx, tx, w); err != nil {
		return Result{}, err
	}
	if result.Messages, err = writeMessages(ctx, tx, w); err != nil {
		return Result{}, err
	}
	if result.Contacts, err = writeContacts(ctx, tx, w); err != nil {
		return Result{}, err
	}
	if result.Aliases, err = writeAliases(ctx, tx, w); err != nil {
		return Result{}, err
	}
	if _, err := io.WriteString(w, "\n}\n"); err != nil {
		return Result{}, err
	}
	if err := w.Flush(); err != nil {
		return Result{}, fmt.Errorf("flush export: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("finish archive snapshot: %w", err)
	}
	return result, nil
}

func writeArray[T any](w *bufio.Writer, name string, rows *sql.Rows, scan func(*sql.Rows) (T, error)) (int, error) {
	defer rows.Close()
	if _, err := fmt.Fprintf(w, ",\n  %q: [", name); err != nil {
		return 0, err
	}
	count := 0
	for rows.Next() {
		value, err := scan(rows)
		if err != nil {
			return 0, fmt.Errorf("scan %s: %w", name, err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return 0, fmt.Errorf("encode %s: %w", name, err)
		}
		separator := "\n    "
		if count > 0 {
			separator = ",\n    "
		}
		if _, err := w.WriteString(separator); err != nil {
			return 0, err
		}
		if _, err := w.Write(encoded); err != nil {
			return 0, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read %s: %w", name, err)
	}
	if count > 0 {
		_, err := w.WriteString("\n  ]")
		return count, err
	}
	_, err := w.WriteString("]")
	return count, err
}

func writeConversations(ctx context.Context, tx *sql.Tx, w *bufio.Writer) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT conversation_id, source_platform, name, is_group, participants_json,
		       last_message_ts, unread, pinned, archived, updated_at
		  FROM conversations
		 ORDER BY last_message_ts, conversation_id`)
	if err != nil {
		return 0, fmt.Errorf("query conversations: %w", err)
	}
	return writeArray(w, "conversations", rows, func(r *sql.Rows) (conversation, error) {
		var c conversation
		var isGroup, unread, pinned, archived, updated int64
		var participants string
		err := r.Scan(&c.ID, &c.SourcePlatform, &c.Name, &isGroup, &participants,
			&c.LastMessageTimeMS, &unread, &pinned, &archived, &updated)
		c.IsGroup, c.Unread, c.Pinned, c.Archived = isGroup != 0, unread != 0, pinned != 0, archived != 0
		c.UpdatedAt = time.UnixMilli(updated).UTC()
		c.Participants = jsonValue(participants)
		return c, err
	})
}

func writeMessages(ctx context.Context, tx *sql.Tx, w *bufio.Writer) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT message_id, conversation_id, source_platform, sender_id, body,
		       timestamp_ms, status, is_from_me, media_id, mime_type,
		       reactions_json, reply_to_id
		  FROM messages
		 ORDER BY timestamp_ms, message_id`)
	if err != nil {
		return 0, fmt.Errorf("query messages: %w", err)
	}
	return writeArray(w, "messages", rows, func(r *sql.Rows) (message, error) {
		var m message
		var body, mediaID, mimeType, reactions, replyTo sql.NullString
		var fromMe int64
		err := r.Scan(&m.ID, &m.ConversationID, &m.SourcePlatform, &m.SenderID, &body,
			&m.TimestampMS, &m.Status, &fromMe, &mediaID, &mimeType, &reactions, &replyTo)
		m.IsFromMe = fromMe != 0
		m.Body, m.MediaID, m.MimeType, m.ReplyToID = stringPtr(body), stringPtr(mediaID), stringPtr(mimeType), stringPtr(replyTo)
		if reactions.Valid {
			value := jsonValue(reactions.String)
			m.Reactions = &value
		}
		return m, err
	})
}

func writeContacts(ctx context.Context, tx *sql.Tx, w *bufio.Writer) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT c.participant_id, c.source_platform, c.contact_id, c.name, c.e164,
		       c.formatted_number, c.avatar_color, c.is_me,
		       COALESCE(a.alias, '')
		  FROM contacts c
		  LEFT JOIN aliases a ON a.target_type = 'contact' AND a.target_id = c.participant_id
		 ORDER BY c.participant_id`)
	if err != nil {
		return 0, fmt.Errorf("query contacts: %w", err)
	}
	return writeArray(w, "contacts", rows, func(r *sql.Rows) (store.Contact, error) {
		var c store.Contact
		var isMe int64
		err := r.Scan(&c.ParticipantID, &c.SourcePlatform, &c.ContactID, &c.Name, &c.E164,
			&c.FormattedNumber, &c.AvatarColor, &isMe, &c.Alias)
		c.IsMe = isMe != 0
		c.DisplayName = c.Name
		if c.Alias != "" {
			c.DisplayName = c.Alias
		}
		return c, err
	})
}

func writeAliases(ctx context.Context, tx *sql.Tx, w *bufio.Writer) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT target_type, target_id, alias, updated_at
		  FROM aliases
		 ORDER BY target_type, target_id`)
	if err != nil {
		return 0, fmt.Errorf("query aliases: %w", err)
	}
	return writeArray(w, "aliases", rows, func(r *sql.Rows) (store.Alias, error) {
		var a store.Alias
		var target string
		var updated int64
		err := r.Scan(&target, &a.TargetID, &a.Alias, &updated)
		a.TargetType = store.AliasTarget(target)
		a.UpdatedAt = time.UnixMilli(updated).UTC()
		return a, err
	})
}

func stringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	v := value.String
	return &v
}

// jsonValue emits protocol JSON as a nested value when valid. If an upstream
// change or damaged row contains malformed JSON, it preserves the original
// bytes as a JSON string instead of silently dropping archive data.
type jsonValue string

func (value jsonValue) MarshalJSON() ([]byte, error) {
	raw := []byte(value)
	if json.Valid(raw) {
		return raw, nil
	}
	return json.Marshal(string(value))
}
