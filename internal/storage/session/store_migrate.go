// Package session persists agent conversations to SQLite and
// reconstructs them on resume. F2.c covers sessions + messages;
// checkpointing lands in F2.d.
package session

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id            TEXT PRIMARY KEY,
			cwd           TEXT NOT NULL,
			title         TEXT NOT NULL DEFAULT '',
			model         TEXT NOT NULL,
			provider      TEXT NOT NULL DEFAULT '',
			reasoning_effort TEXT NOT NULL DEFAULT '',
			parent_id     TEXT,
			created_at    INTEGER NOT NULL,
			updated_at    INTEGER NOT NULL,
			message_count INTEGER NOT NULL DEFAULT 0,
			token_in      INTEGER NOT NULL DEFAULT 0,
			token_out     INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (parent_id) REFERENCES sessions(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_list ON sessions(updated_at DESC, created_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_cwd_list ON sessions(cwd, updated_at DESC, created_at DESC, id DESC)`,
		// The composite indexes subsume the old single-column indexes. Dropping
		// them avoids paying for four index updates on every appended message.
		`DROP INDEX IF EXISTS idx_sessions_updated`,
		`DROP INDEX IF EXISTS idx_sessions_cwd`,
		`CREATE TABLE IF NOT EXISTS prompt_queue (
			id         TEXT PRIMARY KEY,
			cwd        TEXT NOT NULL,
			session_id TEXT,
			prompt     TEXT NOT NULL,
			position   INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_prompt_queue_cwd ON prompt_queue(cwd, position)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id      TEXT NOT NULL,
			seq             INTEGER NOT NULL,
			role            TEXT NOT NULL,
			content         TEXT,
			parts_json      TEXT,
			tool_call_id    TEXT,
			tool_calls_json TEXT,
			name            TEXT,
			created_at      INTEGER NOT NULL,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
			UNIQUE (session_id, seq)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, seq)`,
		`CREATE TABLE IF NOT EXISTS message_attachments (
			session_id TEXT NOT NULL,
			user_seq   INTEGER NOT NULL,
			paths_json TEXT NOT NULL DEFAULT '[]',
			created_at INTEGER NOT NULL,
			PRIMARY KEY (session_id, user_seq),
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS session_context_projections (
			session_id    TEXT PRIMARY KEY,
			through_seq   INTEGER NOT NULL,
			messages_json BLOB NOT NULL,
			updated_at    INTEGER NOT NULL,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS session_usage (
			id                    INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id            TEXT NOT NULL,
			call_seq              INTEGER NOT NULL,
			provider              TEXT NOT NULL DEFAULT '',
			provider_type         TEXT NOT NULL DEFAULT '',
			endpoint_host         TEXT NOT NULL DEFAULT '',
			model                 TEXT NOT NULL DEFAULT '',
			input_tokens          INTEGER NOT NULL DEFAULT 0,
			output_tokens         INTEGER NOT NULL DEFAULT 0,
			cached_input_tokens   INTEGER NOT NULL DEFAULT 0,
			reasoning_tokens      INTEGER NOT NULL DEFAULT 0,
			has_cached_input      INTEGER NOT NULL DEFAULT 0,
			has_reasoning         INTEGER NOT NULL DEFAULT 0,
			context_window        INTEGER NOT NULL DEFAULT 0,
			ctx_system_tokens     INTEGER NOT NULL DEFAULT 0,
			ctx_user_tokens       INTEGER NOT NULL DEFAULT 0,
			ctx_assistant_tokens  INTEGER NOT NULL DEFAULT 0,
			ctx_tool_tokens       INTEGER NOT NULL DEFAULT 0,
			ctx_other_tokens      INTEGER NOT NULL DEFAULT 0,
			source                TEXT NOT NULL DEFAULT 'model',
			created_at            INTEGER NOT NULL,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
			UNIQUE (session_id, call_seq)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_session_usage_session ON session_usage(session_id, call_seq)`,
		`CREATE INDEX IF NOT EXISTS idx_session_usage_created ON session_usage(created_at)`,
		`CREATE TABLE IF NOT EXISTS session_turns (
			id                    INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id            TEXT NOT NULL,
			assistant_seq         INTEGER NOT NULL,
			duration_ms           INTEGER NOT NULL DEFAULT 0,
			input_tokens          INTEGER NOT NULL DEFAULT 0,
			output_tokens         INTEGER NOT NULL DEFAULT 0,
			cached_input_tokens   INTEGER NOT NULL DEFAULT 0,
			reasoning_tokens      INTEGER NOT NULL DEFAULT 0,
			has_cached_input      INTEGER NOT NULL DEFAULT 0,
			has_reasoning         INTEGER NOT NULL DEFAULT 0,
			tool_calls            INTEGER NOT NULL DEFAULT 0,
			tool_failures         INTEGER NOT NULL DEFAULT 0,
			steps                 INTEGER NOT NULL DEFAULT 0,
			model_calls           INTEGER NOT NULL DEFAULT 0,
			failed_model_calls    INTEGER NOT NULL DEFAULT 0,
			canceled_model_calls  INTEGER NOT NULL DEFAULT 0,
			background_calls      INTEGER NOT NULL DEFAULT 0,
			helper_calls          INTEGER NOT NULL DEFAULT 0,
			aux_calls             INTEGER NOT NULL DEFAULT 0,
			aux_us                INTEGER NOT NULL DEFAULT 0,
			phases_json           TEXT NOT NULL DEFAULT '{}',
			file_changes_json     TEXT NOT NULL DEFAULT '[]',
			created_at            INTEGER NOT NULL,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
			UNIQUE (session_id, assistant_seq)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_session_turns_session ON session_turns(session_id, assistant_seq)`,
		`CREATE INDEX IF NOT EXISTS idx_session_turns_created ON session_turns(created_at)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("exec %q: %w", firstLine(q), err)
		}
	}
	for _, column := range []struct {
		name string
		def  string
	}{
		{"provider", "TEXT NOT NULL DEFAULT ''"},
		{"reasoning_effort", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureSessionColumn(column.name, column.def); err != nil {
			return err
		}
	}
	// Enrich the existing one-row-per-answer summary. This adds no second
	// telemetry stream and therefore no extra writes during a completed turn.
	for _, column := range []struct {
		name string
		def  string
	}{
		{"tool_failures", "INTEGER NOT NULL DEFAULT 0"},
		{"steps", "INTEGER NOT NULL DEFAULT 0"},
		{"model_calls", "INTEGER NOT NULL DEFAULT 0"},
		{"failed_model_calls", "INTEGER NOT NULL DEFAULT 0"},
		{"canceled_model_calls", "INTEGER NOT NULL DEFAULT 0"},
		{"background_calls", "INTEGER NOT NULL DEFAULT 0"},
		{"helper_calls", "INTEGER NOT NULL DEFAULT 0"},
		{"aux_calls", "INTEGER NOT NULL DEFAULT 0"},
		{"aux_us", "INTEGER NOT NULL DEFAULT 0"},
		{"phases_json", "TEXT NOT NULL DEFAULT '{}'"},
		{"file_changes_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"tool_diag_json", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureTableColumn("session_turns", column.name, column.def); err != nil {
			return err
		}
	}
	// F13: FTS5 index on messages.content with porter stemming +
	// diacritic folding. Triggers keep the index in sync. The
	// 'rebuild' command backfills any rows that pre-existed.
	// We check for the FTS table first so the migration is
	// idempotent and self-healing for stores that predate F13.
	var hasFTS int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='messages_fts'`).Scan(&hasFTS); err != nil {
		return fmt.Errorf("check fts: %w", err)
	}
	if hasFTS == 0 {
		ftsStmts := []string{
			// FTS5 stores its own copy of content (no
			// content_rowid linkage — that path is broken
			// in modernc.org/sqlite 1.52.0 and fails the
			// 'delete' command with SQL logic error).
			`CREATE VIRTUAL TABLE messages_fts USING fts5(
				content,
				tokenize = 'porter unicode61 remove_diacritics 2'
			)`,
			`CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
				INSERT INTO messages_fts(rowid, content) VALUES (new.id, COALESCE(new.content, ''));
			END`,
			`CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
				DELETE FROM messages_fts WHERE rowid = old.id;
			END`,
			`CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
				DELETE FROM messages_fts WHERE rowid = old.id;
				INSERT INTO messages_fts(rowid, content) VALUES (new.id, COALESCE(new.content, ''));
			END`,
		}
		for _, q := range ftsStmts {
			if _, err := s.db.Exec(q); err != nil {
				return fmt.Errorf("exec %q: %w", firstLine(q), err)
			}
		}
	}
	return nil
}

func scanSession(row *sql.Row) (Session, error) {
	var sess Session
	var created, updated int64
	err := row.Scan(&sess.ID, &sess.Cwd, &sess.Title, &sess.Model, &sess.Provider, &sess.ReasoningEffort, &sess.ParentID, &created, &updated, &sess.MessageCount, &sess.TokenIn, &sess.TokenOut)
	if err != nil {
		return Session{}, err
	}
	sess.CreatedAt = time.Unix(0, created).UTC()
	sess.UpdatedAt = time.Unix(0, updated).UTC()
	return sess, nil
}

func scanAll(rows *sql.Rows, limit int) ([]Session, error) {
	var out []Session
	for rows.Next() {
		var sess Session
		var created, updated int64
		if err := rows.Scan(&sess.ID, &sess.Cwd, &sess.Title, &sess.Model, &sess.Provider, &sess.ReasoningEffort, &sess.ParentID, &created, &updated, &sess.MessageCount, &sess.TokenIn, &sess.TokenOut); err != nil {
			return nil, err
		}
		sess.CreatedAt = time.Unix(0, created).UTC()
		sess.UpdatedAt = time.Unix(0, updated).UTC()
		out = append(out, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) ensureSessionColumn(name, definition string) error {
	return s.ensureTableColumn("sessions", name, definition)
}

func (s *Store) ensureTableColumn(table, name, definition string) error {
	if table != "sessions" && table != "session_turns" {
		return fmt.Errorf("unsupported migration table %q", table)
	}
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return fmt.Errorf("%s columns: %w", table, err)
	}
	found := false
	for rows.Next() {
		var cid, notNull, pk int
		var column, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &column, &typ, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("%s columns scan: %w", table, err)
		}
		if column == name {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	if _, err := s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + name + ` ` + definition); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, name, err)
	}
	return nil
}

// newID returns a 16-character hex id.
func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read on modernc.org/sqlite is not used here.
		// We fall back to time-based id, which is good enough
		// for session uniqueness in practice.
		ts := time.Now().UTC().UnixNano()
		return fmt.Sprintf("%016x", ts)
	}
	return hex.EncodeToString(b[:])
}

// nullable returns sql.NullString for an empty string.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ErrNotFound is returned by Get/LastForCwd when the id/cwd
// has no matching row.
var ErrNotFound = sql.ErrNoRows

// IsNotFound reports whether err comes from a missing row.
func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// firstLine returns the first line of s (used to format
// error messages without dumping the full SQL).
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
