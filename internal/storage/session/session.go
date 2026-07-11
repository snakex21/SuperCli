// Package session persists agent conversations to SQLite and
// reconstructs them on resume. F2.c covers sessions + messages;
// checkpointing lands in F2.d.
package session

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Session is metadata for a single conversation.
type Session struct {
	ID           string
	Cwd          string
	Title        string
	Model        string
	ParentID     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int
	TokenIn      int
	TokenOut     int
}

// Store is the SQLite-backed session store. It is safe for
// concurrent use. Close releases the underlying *sql.DB.
type Store struct {
	db   *sql.DB
	root string
}

// OpenStore opens (or creates) a session store inside the given
// home directory. Pass an empty home to use a temp dir.
func OpenStore(home string) (*Store, error) {
	if home == "" {
		var err error
		home, err = tempHome()
		if err != nil {
			return nil, err
		}
	}
	if err := mkdirAll(home, 0o755); err != nil {
		return nil, fmt.Errorf("session.OpenStore: mkdir: %w", err)
	}
	dsn := filepath.Join(home, "sessions.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("session.OpenStore: open: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("session.OpenStore: ping: %w", err)
	}
	s := &Store{db: db, root: home}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("session.OpenStore: migrate: %w", err)
	}
	return s, nil
}

// Close releases the SQLite connection.
//
// We intentionally do not run PRAGMA wal_checkpoint(TRUNCATE) here:
// on Windows that checkpoint can add a visible pause while quitting
// the interactive CLI. SQLite WAL is safe to leave in place; committed
// transactions are recovered from the WAL on the next open.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Root returns the home directory the store was opened with.
func (s *Store) Root() string { return s.root }

// Create starts a new session. A fresh id is generated and
// cwd/model/timestamp are snapshotted.
func (s *Store) Create(cwd, model, title string) (Session, error) {
	return s.CreateWithParent(cwd, model, title, "")
}

// CreateWithParent is Create with a parent_id (for sub-agent
// sessions in F3).
func (s *Store) CreateWithParent(cwd, model, title, parentID string) (Session, error) {
	if cwd == "" {
		return Session{}, fmt.Errorf("session.Store.Create: cwd is empty")
	}
	if model == "" {
		return Session{}, fmt.Errorf("session.Store.Create: model is empty")
	}
	id := newID()
	now := time.Now().UTC()
	ns := now.UnixNano()
	_, err := s.db.Exec(
		`INSERT INTO sessions(id, cwd, title, model, parent_id, created_at, updated_at, message_count, token_in, token_out) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		id, cwd, title, model, nullable(parentID), ns, ns, 0, 0, 0,
	)
	if err != nil {
		return Session{}, fmt.Errorf("session.Store.Create: insert: %w", err)
	}
	return Session{
		ID:        id,
		Cwd:       cwd,
		Title:     title,
		Model:     model,
		ParentID:  parentID,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// EnsureSession inserts a sessions row for a pre-chosen id if one does
// not already exist, snapshotting cwd (and model, best-effort). The live
// TUI picks its session id up front and writes only into the messages
// table via the Writer, so without this the sessions row — and thus the
// cwd needed to attribute a session to a project — never existed. Safe
// to call repeatedly (INSERT OR IGNORE); an empty cwd is rejected.
func (s *Store) EnsureSession(id, cwd, model string) error {
	if id == "" {
		return fmt.Errorf("session.Store.EnsureSession: id is empty")
	}
	if cwd == "" {
		return fmt.Errorf("session.Store.EnsureSession: cwd is empty")
	}
	ns := time.Now().UTC().UnixNano()
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO sessions(id, cwd, title, model, parent_id, created_at, updated_at, message_count, token_in, token_out) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		id, cwd, "", model, nullable(""), ns, ns, 0, 0, 0,
	)
	if err != nil {
		return fmt.Errorf("session.Store.EnsureSession: %w", err)
	}
	return nil
}

// Get returns the session with the given ID.
func (s *Store) Get(id string) (Session, error) {
	row := s.db.QueryRow(
		`SELECT id, cwd, title, model, IFNULL(parent_id,''), created_at, updated_at, message_count, token_in, token_out FROM sessions WHERE id = ?`,
		id,
	)
	return scanSession(row)
}

// List returns up to limit sessions, newest first.
func (s *Store) List(limit int) ([]Session, error) {
	rows, err := s.db.Query(
		`SELECT id, cwd, title, model, IFNULL(parent_id,''), created_at, updated_at, message_count, token_in, token_out FROM sessions ORDER BY updated_at DESC, created_at DESC, rowid DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAll(rows, limit)
}

// ListByCwd returns sessions whose cwd matches exactly, newest
// first.
func (s *Store) ListByCwd(cwd string, limit int) ([]Session, error) {
	rows, err := s.db.Query(
		`SELECT id, cwd, title, model, IFNULL(parent_id,''), created_at, updated_at, message_count, token_in, token_out FROM sessions WHERE cwd = ? ORDER BY updated_at DESC, created_at DESC, rowid DESC`,
		cwd,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAll(rows, limit)
}

// LastForCwd returns the most recent session for cwd, or
// sql.ErrNoRows if none.
func (s *Store) LastForCwd(cwd string) (Session, error) {
	row := s.db.QueryRow(
		`SELECT id, cwd, title, model, IFNULL(parent_id,''), created_at, updated_at, message_count, token_in, token_out FROM sessions WHERE cwd = ? ORDER BY updated_at DESC, created_at DESC, rowid DESC LIMIT 1`,
		cwd,
	)
	return scanSession(row)
}

// SetTitle updates a session's title.
func (s *Store) SetTitle(id, title string) error {
	now := time.Now().UTC()
	res, err := s.db.Exec(`UPDATE sessions SET title = ?, updated_at = ? WHERE id = ?`, title, now.UnixNano(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetTitleIfCurrent updates a generated title only when nobody renamed the
// session in the meantime. It prevents an asynchronous title summarizer from
// overwriting a user's explicit conversation name.
func (s *Store) SetTitleIfCurrent(id, current, title string) (bool, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(`UPDATE sessions SET title = ?, updated_at = ? WHERE id = ? AND title = ?`, title, now.UnixNano(), id, current)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Delete removes a session and cascades to its messages.
func (s *Store) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// AppendMessage adds a message to a session, assigning the next
// seq number. tokenIn/tokenOut are optional (zero is fine).
func (s *Store) AppendMessage(ctx context.Context, sessionID string, msg Encoded) error {
	if sessionID == "" {
		return fmt.Errorf("session.Store.AppendMessage: sessionID is empty")
	}
	if err := msg.Validate(); err != nil {
		return fmt.Errorf("session.Store.AppendMessage: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var nextSeq int
	if err := tx.QueryRow(
		`SELECT IFNULL(MAX(seq), 0) + 1 FROM messages WHERE session_id = ?`, sessionID,
	).Scan(&nextSeq); err != nil {
		return fmt.Errorf("append: next seq: %w", err)
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(
		`INSERT INTO messages(session_id, seq, role, content, parts_json, tool_call_id, tool_calls_json, name, created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		sessionID, nextSeq, msg.Role, msg.Content, msg.PartsJSON, msg.ToolCallID, msg.ToolCallsJSON, msg.Name, now.UnixNano(),
	); err != nil {
		return fmt.Errorf("append: insert: %w", err)
	}
	if _, err := tx.Exec(
		`UPDATE sessions SET message_count = message_count + 1, updated_at = ? WHERE id = ?`,
		now.UnixNano(), sessionID,
	); err != nil {
		return fmt.Errorf("append: bump: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// RecentSession is a /resume listing entry. It is derived from
// the messages table directly (GROUP BY session_id) so sessions
// written by the F13 writer — which never created a sessions
// row — still show up.
type RecentSession struct {
	ID           string
	StartedAt    time.Time
	FirstUserMsg string
	MessageCount int
	// Cwd is the working directory recorded for the session (from the
	// sessions row), or "" when no sessions row exists (F13 writer
	// sessions are message-only). Populated via a LEFT JOIN so the
	// listing can attribute a session to a project.
	Cwd string
}

// ListRecent returns up to limit sessions that have at least
// one message, most recent first, with the first user message
// as a snippet source. Sessions from any project are included.
func (s *Store) ListRecent(ctx context.Context, limit int) ([]RecentSession, error) {
	return s.listRecent(ctx, "", limit)
}

// ListRecentByCwd is ListRecent filtered to sessions whose recorded
// working directory equals cwd — the backing for the TUI "current
// project" session view. Sessions with no sessions row (message-only)
// have an unknown cwd and are therefore excluded from a project filter;
// they still appear in the unfiltered ListRecent ("all") view. An empty
// cwd disables the filter and behaves like ListRecent.
func (s *Store) ListRecentByCwd(ctx context.Context, cwd string, limit int) ([]RecentSession, error) {
	return s.listRecent(ctx, cwd, limit)
}

func (s *Store) listRecent(ctx context.Context, cwd string, limit int) ([]RecentSession, error) {
	if limit <= 0 {
		limit = 10
	}
	// LEFT JOIN sessions so the cwd is available for display and
	// filtering while message-only sessions (no sessions row) still
	// appear in the unfiltered view.
	query := `
		SELECT m.session_id,
		       MIN(m.created_at) AS started,
		       COUNT(*) AS n,
		       IFNULL((SELECT content FROM messages
		               WHERE session_id = m.session_id AND role = 'user'
		                 AND content IS NOT NULL AND content <> ''
		               ORDER BY seq LIMIT 1), ''),
		       IFNULL(s.cwd, '')
		FROM messages m
		LEFT JOIN sessions s ON s.id = m.session_id`
	args := []any{}
	if cwd != "" {
		query += `
		WHERE s.cwd = ?`
		args = append(args, cwd)
	}
	query += `
		GROUP BY m.session_id
		ORDER BY started DESC
		LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("session.Store.ListRecent: %w", err)
	}
	defer rows.Close()
	var out []RecentSession
	for rows.Next() {
		var r RecentSession
		var startedNanos int64
		if err := rows.Scan(&r.ID, &startedNanos, &r.MessageCount, &r.FirstUserMsg, &r.Cwd); err != nil {
			return nil, fmt.Errorf("session.Store.ListRecent: scan: %w", err)
		}
		r.StartedAt = time.Unix(0, startedNanos).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReadMessages returns all messages for a session, in seq order.
func (s *Store) ReadMessages(ctx context.Context, sessionID string) ([]Encoded, error) {
	rows, err := s.db.Query(
		`SELECT session_id, seq, role, content, IFNULL(parts_json,''), IFNULL(tool_call_id,''), IFNULL(tool_calls_json,''), IFNULL(name,'') FROM messages WHERE session_id = ? ORDER BY seq ASC`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Encoded
	for rows.Next() {
		var m Encoded
		if err := rows.Scan(&m.SessionID, &m.Seq, &m.Role, &m.Content, &m.PartsJSON, &m.ToolCallID, &m.ToolCallsJSON, &m.Name); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateUsage updates the cumulative token counters for a
// session. It is called after the loop emits a DoneEvent.
func (s *Store) UpdateUsage(sessionID string, in, out int) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET token_in = token_in + ?, token_out = token_out + ?, updated_at = ? WHERE id = ?`,
		in, out, time.Now().UTC().UnixNano(), sessionID,
	)
	return err
}

// HistoryHit is one row returned by SearchHistory. F13.
type HistoryHit struct {
	SessionID string
	Seq       int
	Role      string
	// Snippet is the FTS5 snippet() output: a short excerpt of
	// content with match terms wrapped in <mark>...</mark>.
	Snippet string
	// CreatedAt is the message timestamp in UTC.
	CreatedAt time.Time
}

// SearchHistory runs an FTS5 query against messages.content.
// Filters (all optional, all combine with AND):
//
//	sessionID — limit to a single session
//	role      — one of "system" | "user" | "assistant" | "tool"
//	since, until — created_at range (UTC, half-open: since <= t < until)
//
//	limit — defaults to 20, capped at 100
//
// Results are ordered by FTS5 rank then created_at DESC.
// An empty query returns an error (use ReadMessages for full
// dumps). The caller is responsible for any escaping; the
// FTS5 MATCH operator accepts a query expression like
//
//	"konspekt OR refaktoryzacja"   (boolean)
//	"\"exact phrase\""             (exact)
//	"prefix*"                      (prefix)
//
// but does NOT accept raw LIKE wildcards.
func (s *Store) SearchHistory(ctx context.Context, query, sessionID, role string, since, until time.Time, limit int) ([]HistoryHit, error) {
	if query == "" {
		return nil, fmt.Errorf("session.Store.SearchHistory: query is empty")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	where := []string{"m.id = f.rowid"}
	args := []any{query}
	if sessionID != "" {
		where = append(where, "m.session_id = ?")
		args = append(args, sessionID)
	}
	if role != "" {
		where = append(where, "m.role = ?")
		args = append(args, role)
	}
	if !since.IsZero() {
		where = append(where, "m.created_at >= ?")
		args = append(args, since.UTC().UnixNano())
	}
	if !until.IsZero() {
		where = append(where, "m.created_at <= ?")
		args = append(args, until.UTC().UnixNano())
	}
	q := fmt.Sprintf(`
		SELECT m.session_id, m.seq, m.role,
		       snippet(messages_fts, 0, '<mark>', '</mark>', '...', 16),
		       m.created_at
		FROM messages_fts f, messages m
		WHERE messages_fts MATCH ? AND %s
		ORDER BY rank, m.created_at DESC
		LIMIT ?
	`, strings.Join(where, " AND "))
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("session.Store.SearchHistory: query: %w", err)
	}
	defer rows.Close()
	out := make([]HistoryHit, 0, limit)
	for rows.Next() {
		var h HistoryHit
		var created int64
		if err := rows.Scan(&h.SessionID, &h.Seq, &h.Role, &h.Snippet, &created); err != nil {
			return nil, fmt.Errorf("session.Store.SearchHistory: scan: %w", err)
		}
		h.CreatedAt = time.Unix(0, created).UTC()
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session.Store.SearchHistory: rows: %w", err)
	}
	return out, nil
}

// migrate creates the sessions and messages tables.
func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id            TEXT PRIMARY KEY,
			cwd           TEXT NOT NULL,
			title         TEXT NOT NULL DEFAULT '',
			model         TEXT NOT NULL,
			parent_id     TEXT,
			created_at    INTEGER NOT NULL,
			updated_at    INTEGER NOT NULL,
			message_count INTEGER NOT NULL DEFAULT 0,
			token_in      INTEGER NOT NULL DEFAULT 0,
			token_out     INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (parent_id) REFERENCES sessions(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_cwd ON sessions(cwd)`,
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
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("exec %q: %w", firstLine(q), err)
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
	err := row.Scan(&sess.ID, &sess.Cwd, &sess.Title, &sess.Model, &sess.ParentID, &created, &updated, &sess.MessageCount, &sess.TokenIn, &sess.TokenOut)
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
		if err := rows.Scan(&sess.ID, &sess.Cwd, &sess.Title, &sess.Model, &sess.ParentID, &created, &updated, &sess.MessageCount, &sess.TokenIn, &sess.TokenOut); err != nil {
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
