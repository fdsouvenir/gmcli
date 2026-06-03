package store

// schemaVersion is the migration target. Bump when migrations[] grows.
const schemaVersion = 3

// migrations are applied in order. Each runs in its own transaction; the
// store records the highest applied version in the schema_version table.
var migrations = []string{
	// v1: initial schema.
	`
	CREATE TABLE schema_version (
		version INTEGER PRIMARY KEY
	);

	-- conversations is keyed on conversation_id from libgm. source_platform
	-- is reserved for a future multi-platform expansion (see research doc
	-- §9). For Google Messages, source_platform = 'gm'.
	CREATE TABLE conversations (
		conversation_id   TEXT PRIMARY KEY,
		source_platform   TEXT NOT NULL DEFAULT 'gm',
		name              TEXT NOT NULL DEFAULT '',
		is_group          INTEGER NOT NULL DEFAULT 0,
		participants_json TEXT NOT NULL DEFAULT '[]',
		last_message_ts   INTEGER NOT NULL DEFAULT 0,
		unread            INTEGER NOT NULL DEFAULT 0,
		pinned            INTEGER NOT NULL DEFAULT 0,
		archived          INTEGER NOT NULL DEFAULT 0,
		updated_at        INTEGER NOT NULL
	) STRICT;

	CREATE INDEX ix_conversations_last_message_ts
		ON conversations (last_message_ts DESC);

	-- messages stores one row per Google Messages message_id. Body is the
	-- text content (NULL for media-only messages). media_id / mime_type /
	-- decryption_key let us defer attachment download to a later command.
	CREATE TABLE messages (
		message_id       TEXT PRIMARY KEY,
		conversation_id  TEXT NOT NULL REFERENCES conversations(conversation_id) ON DELETE CASCADE,
		source_platform  TEXT NOT NULL DEFAULT 'gm',
		sender_id        TEXT NOT NULL DEFAULT '',
		body             TEXT,
		timestamp_ms     INTEGER NOT NULL,
		status           INTEGER NOT NULL DEFAULT 0,
		is_from_me       INTEGER NOT NULL DEFAULT 0,
		media_id         TEXT,
		mime_type        TEXT,
		decryption_key   BLOB,
		reactions_json   TEXT,
		reply_to_id      TEXT,
		raw_proto        BLOB,
		updated_at       INTEGER NOT NULL
	) STRICT;

	CREATE INDEX ix_messages_conv_ts
		ON messages (conversation_id, timestamp_ms);
	CREATE INDEX ix_messages_ts
		ON messages (timestamp_ms DESC);
	CREATE UNIQUE INDEX ux_messages_source
		ON messages (source_platform, message_id);

	-- contacts is the address book. participant_id is libgm's stable ID;
	-- contact_id is Google's contact-database ID (may differ for the same
	-- person across platforms, hence the separate column).
	CREATE TABLE contacts (
		participant_id   TEXT PRIMARY KEY,
		source_platform  TEXT NOT NULL DEFAULT 'gm',
		contact_id       TEXT NOT NULL DEFAULT '',
		name             TEXT NOT NULL DEFAULT '',
		e164             TEXT NOT NULL DEFAULT '',
		formatted_number TEXT NOT NULL DEFAULT '',
		avatar_color     TEXT NOT NULL DEFAULT '',
		is_me            INTEGER NOT NULL DEFAULT 0,
		updated_at       INTEGER NOT NULL
	) STRICT;

	CREATE INDEX ix_contacts_e164 ON contacts (e164);
	CREATE INDEX ix_contacts_name ON contacts (name);

	-- FTS5 mirror of message bodies for fuzzy search. Trigram tokenizer
	-- handles partial-word matches reasonably for chat-style content.
	-- Synchronized in code via insert/update triggers below.
	CREATE VIRTUAL TABLE messages_fts USING fts5(
		message_id UNINDEXED,
		body,
		tokenize = 'trigram'
	);

	CREATE TRIGGER messages_fts_ai AFTER INSERT ON messages BEGIN
		INSERT INTO messages_fts(message_id, body)
		VALUES (new.message_id, COALESCE(new.body, ''));
	END;

	CREATE TRIGGER messages_fts_ad AFTER DELETE ON messages BEGIN
		DELETE FROM messages_fts WHERE message_id = old.message_id;
	END;

	CREATE TRIGGER messages_fts_au AFTER UPDATE OF body ON messages BEGIN
		DELETE FROM messages_fts WHERE message_id = old.message_id;
		INSERT INTO messages_fts(message_id, body)
		VALUES (new.message_id, COALESCE(new.body, ''));
	END;

	-- Single-row table tracking the most recent successful sync. Used by
	-- the doctor command and as a freshness signal for the LLM.
	CREATE TABLE sync_state (
		id              INTEGER PRIMARY KEY CHECK (id = 1),
		last_event_ts   INTEGER NOT NULL DEFAULT 0,
		last_connect_ts INTEGER NOT NULL DEFAULT 0,
		updated_at      INTEGER NOT NULL
	) STRICT;
	INSERT INTO sync_state (id, updated_at) VALUES (1, 0);

	INSERT INTO schema_version (version) VALUES (1);
	`,
	// v2: local-only contact and conversation aliases. Aliases are user
	// labels that override the libgm-supplied name in display contexts.
	// They are intentionally local — never sent back to Google — so the
	// user can call a participant "Mom" without affecting their address
	// book on the phone. Modeled on wacli's contacts alias verb.
	`
	CREATE TABLE aliases (
		target_type   TEXT NOT NULL CHECK (target_type IN ('contact','conversation')),
		target_id     TEXT NOT NULL,
		alias         TEXT NOT NULL,
		updated_at    INTEGER NOT NULL,
		PRIMARY KEY (target_type, target_id)
	) STRICT;

	INSERT INTO schema_version (version) VALUES (2);
	`,
	// v3: latest phone settings cache. Google Messages send requests need
	// SIM metadata from Settings; caching the last event lets short-lived
	// send commands avoid depending on receiving Settings during startup.
	`
	CREATE TABLE phone_settings (
		id         INTEGER PRIMARY KEY CHECK (id = 1),
		raw_proto  BLOB NOT NULL,
		sim_count  INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL
	) STRICT;

	INSERT INTO schema_version (version) VALUES (3);
	`,
}
