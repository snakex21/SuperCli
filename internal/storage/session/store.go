// Package session persists agent conversations to SQLite and
// reconstructs them on resume. F2.c covers sessions + messages;
// checkpointing lands in F2.d.
package session

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Session is metadata for a single conversation.
type Session struct {
	ID              string
	Cwd             string
	Title           string
	Model           string
	Provider        string
	ReasoningEffort string
	ParentID        string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	MessageCount    int
	TokenIn         int
	TokenOut        int
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
		`SELECT id, cwd, title, model, provider, reasoning_effort, IFNULL(parent_id,''), created_at, updated_at, message_count, token_in, token_out FROM sessions WHERE id = ?`,
		id,
	)
	return scanSession(row)
}

// List returns up to limit sessions, newest first.
func (s *Store) List(limit int) ([]Session, error) {
	query := `SELECT id, cwd, title, model, provider, reasoning_effort, IFNULL(parent_id,''), created_at, updated_at, message_count, token_in, token_out FROM sessions ORDER BY updated_at DESC, created_at DESC, id DESC`
	var args []any
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAll(rows, 0)
}

// ListByCwd returns sessions whose cwd matches exactly, newest
// first.
func (s *Store) ListByCwd(cwd string, limit int) ([]Session, error) {
	query := `SELECT id, cwd, title, model, provider, reasoning_effort, IFNULL(parent_id,''), created_at, updated_at, message_count, token_in, token_out FROM sessions WHERE cwd = ? ORDER BY updated_at DESC, created_at DESC, id DESC`
	args := []any{cwd}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAll(rows, 0)
}

// LastForCwd returns the most recent session for cwd, or
// sql.ErrNoRows if none.
func (s *Store) LastForCwd(cwd string) (Session, error) {
	row := s.db.QueryRow(
		`SELECT id, cwd, title, model, provider, reasoning_effort, IFNULL(parent_id,''), created_at, updated_at, message_count, token_in, token_out FROM sessions WHERE cwd = ? ORDER BY updated_at DESC, created_at DESC, id DESC LIMIT 1`,
		cwd,
	)
	return scanSession(row)
}

// SetRuntime snapshots the provider/model/reasoning selection actually used by
// the next turn. It does not bump UpdatedAt: opening or preparing a session is
// not conversation activity, and AppendMessage will update the timestamp once
// the user really sends something.
func (s *Store) SetRuntime(id, provider, model, reasoningEffort string) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(model) == "" {
		return fmt.Errorf("session.Store.SetRuntime: id and model are required")
	}
	res, err := s.db.Exec(
		`UPDATE sessions SET provider = ?, model = ?, reasoning_effort = ? WHERE id = ?`,
		strings.TrimSpace(provider), strings.TrimSpace(model), strings.TrimSpace(reasoningEffort), id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
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

// DeleteAll removes every conversation and its cascaded messages, turns and
// usage rows. Provider configuration and project files are not stored here.
func (s *Store) DeleteAll() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM prompt_queue`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions`); err != nil {
		return err
	}
	return tx.Commit()
}

// ReassignCwd moves durable conversation and queue ownership from one
// workspace path to another. It is used when a project is still the same
// folder but its absolute path changes, for example after Windows gives a USB
// drive a different letter.
func (s *Store) ReassignCwd(oldCwd, newCwd string) error {
	oldCwd = strings.TrimSpace(oldCwd)
	newCwd = strings.TrimSpace(newCwd)
	if oldCwd == "" || newCwd == "" {
		return fmt.Errorf("session.Store.ReassignCwd: old and new cwd are required")
	}
	if oldCwd == newCwd {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var queueOffset int
	if err := tx.QueryRow(`SELECT IFNULL(MAX(position), 0) FROM prompt_queue WHERE cwd = ?`, newCwd).Scan(&queueOffset); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE prompt_queue SET cwd = ?, position = position + ? WHERE cwd = ?`, newCwd, queueOffset, oldCwd); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE sessions SET cwd = ? WHERE cwd = ?`, newCwd, oldCwd); err != nil {
		return err
	}
	return tx.Commit()
}

// TruncateFrom permanently removes transcript messages at and after fromSeq
// from one session. It is the storage primitive behind the WebGUI's simple
// in-place rewind: the conversation keeps its identity and no branch is
// created. Historical usage remains intact because tokens already consumed
// are still real usage, while transcript-derived projections and turn
// summaries are invalidated so a resumed model cannot see removed content.
