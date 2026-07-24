package memory

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	// These are live-store safety rails, not prompt budgets. SQLite/FTS/vector
	// overhead varies by embedder, so bounding both rows and source text keeps
	// the complete store comfortably below multi-gigabyte territory.
	MaxStoreEntries      = 4096
	MaxStoreContentBytes = 32 * 1024 * 1024

	MaxTaskLogEntries      = 200
	MaxRawLogEntries       = 3
	MaxDailyScratchEntries = 200
)

// Store is the persistent memory store: SQLite for queries
// (with FTS5 search) plus a markdown file mirror for human
// inspection. The two are kept in sync by every Put/Delete.
//
// A Store is safe for concurrent use. Close releases the
// underlying *sql.DB.
type Store struct {
	db       *sql.DB
	root     string
	mirrorMu *sync.Mutex
	// writeMu serializes mutations and outbox draining within this Store. SQLite
	// generations also make independently opened Store instances converge when
	// they render the same scope concurrently.
	writeMu sync.Mutex
	// beforeMirrorWrite is a test-only fault-injection hook. Production stores
	// leave it nil; keeping the failure point here lets recovery tests exercise
	// a committed SQLite mutation followed by an unavailable filesystem.
	beforeMirrorWrite func(scope string) error
	afterMirrorRead   func(scope string)
	afterStaleRead    func()

	// Optional embedding backend for hybrid search (hybrid.go).
	// Guarded by embedMu because detection runs in a background
	// goroutine at startup.
	embedMu  sync.RWMutex
	embedder Embedder
}

// OpenStore opens (or creates) a persistent memory store inside
// the given home directory. The SQLite database is
// `<root>/memory.db`; the markdown files live in `<root>/memory/`.
//
// Pass an empty home to use a temporary directory (handy in
// tests). The caller is responsible for Close.
func OpenStore(home string) (*Store, error) {
	if home == "" {
		var err error
		home, err = tempHome()
		if err != nil {
			return nil, err
		}
	}
	if err := mkdirAll(filepath.Join(home, "memory"), 0o755); err != nil {
		return nil, fmt.Errorf("memory.OpenStore: mkdir memory/: %w", err)
	}
	if err := mkdirAll(filepath.Join(home, "memory", "patterns"), 0o755); err != nil {
		return nil, fmt.Errorf("memory.OpenStore: mkdir patterns/: %w", err)
	}
	if err := mkdirAll(filepath.Join(home, "memory", "archive"), 0o755); err != nil {
		return nil, fmt.Errorf("memory.OpenStore: mkdir archive/: %w", err)
	}
	// IMMEDIATE transactions acquire SQLite's single-writer reservation before
	// reading. Mirror rendering deliberately holds that reservation from outbox
	// selection through the atomic Markdown replace and acknowledgement, which
	// also serializes renderers living in different SuperCli processes.
	dsn := filepath.Join(home, "memory.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("memory.OpenStore: open: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("memory.OpenStore: ping: %w", err)
	}
	s := &Store{db: db, root: home, mirrorMu: processMirrorLock(home)}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("memory.OpenStore: migrate: %w", err)
	}
	if err := s.reconcileMarkdown(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("memory.OpenStore: reconcile markdown: %w", err)
	}
	return s, nil
}

// Close releases the SQLite connection.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Root returns the home directory the store was opened with.
func (s *Store) Root() string { return s.root }

// migrate creates the memory tables and the FTS5 virtual
// table. FTS5 is part of modernc.org/sqlite so the
// CREATE VIRTUAL TABLE statement is safe to run unconditionally.
func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS memory_entries (
			id          TEXT PRIMARY KEY,
			scope       TEXT NOT NULL,
			file_path   TEXT NOT NULL,
			line_start  INTEGER NOT NULL DEFAULT 0,
			line_end    INTEGER NOT NULL DEFAULT 0,
			content     TEXT NOT NULL,
			tags        TEXT NOT NULL DEFAULT '',
			source      TEXT NOT NULL DEFAULT 'user',
			created_at  INTEGER NOT NULL,
			updated_at  INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_scope ON memory_entries(scope)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_updated ON memory_entries(updated_at DESC)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
			id UNINDEXED,
			scope UNINDEXED,
			content,
			tags,
			tokenize = 'unicode61 remove_diacritics 2'
		)`,
		`CREATE TABLE IF NOT EXISTS memory_scratch_archive (
			date        TEXT PRIMARY KEY,
			path        TEXT NOT NULL,
			archived_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS memory_mirror_outbox (
			scope       TEXT PRIMARY KEY,
			generation  INTEGER NOT NULL DEFAULT 1,
			enqueued_at INTEGER NOT NULL
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("exec %q: %w", firstLine(q), err)
		}
	}
	return nil
}

// Get returns the entry with the given ID.
func (s *Store) Get(id string) (Entry, error) {
	row := s.db.QueryRow(`SELECT id, scope, file_path, line_start, line_end, content, tags, source, created_at, updated_at FROM memory_entries WHERE id = ?`, id)
	return scanEntry(row)
}

// Put commits SQLite and its mirror outbox first, then atomically regenerates
// Markdown. A failed filesystem step remains queued for startup recovery.
func (s *Store) Put(e Entry) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := e.Validate(); err != nil {
		return err
	}
	if err := s.ensureCapacity(e); err != nil {
		return err
	}
	if e.Source == "" {
		e.Source = SourceUser
	}
	now := time.Now().UTC()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	e.UpdatedAt = now

	filePath, _, err := ScopeFile(s.markdownRoot(), e.Scope)
	if err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var oldScope string
	if err := tx.QueryRow(`SELECT scope FROM memory_entries WHERE id = ?`, e.ID).Scan(&oldScope); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read old scope: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM memory_entries WHERE id = ?`, e.ID); err != nil {
		return fmt.Errorf("delete old: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM memory_fts WHERE id = ?`, e.ID); err != nil {
		return fmt.Errorf("delete fts: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO memory_entries(id, scope, file_path, line_start, line_end, content, tags, source, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.Scope, filePath, 0, 0, e.Content, e.TagsCSV(), e.Source, e.CreatedAt.Unix(), e.UpdatedAt.Unix(),
	); err != nil {
		return fmt.Errorf("insert entry: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO memory_fts(id, scope, content, tags) VALUES (?,?,?,?)`,
		e.ID, e.Scope, e.Content, strings.Join(e.Tags, " "),
	); err != nil {
		return fmt.Errorf("insert fts: %w", err)
	}
	if err := enqueueMirrorTx(tx, oldScope); err != nil {
		return fmt.Errorf("enqueue old mirror: %w", err)
	}
	if err := enqueueMirrorTx(tx, e.Scope); err != nil {
		return fmt.Errorf("enqueue mirror: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Best-effort vector indexing (hybrid.go) — never fails Put.
	s.afterPut(e)
	if err := s.drainMirrorOutboxLocked(); err != nil {
		return fmt.Errorf("memory.Store.Put(%s): %w", e.ID, err)
	}
	return nil
}

func (s *Store) ensureCapacity(e Entry) error {
	var entries, contentBytes int64
	err := s.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(length(CAST(content AS BLOB))), 0)
		FROM memory_entries WHERE id <> ?`, e.ID).Scan(&entries, &contentBytes)
	if err != nil {
		return fmt.Errorf("memory.Store.Put(%s): capacity check: %w", e.ID, err)
	}
	if entries+1 > MaxStoreEntries {
		return fmt.Errorf("memory.Store.Put(%s): store entry limit %d reached; delete or compact old memories before saving more", e.ID, MaxStoreEntries)
	}
	if contentBytes+int64(len(e.Content)) > MaxStoreContentBytes {
		return fmt.Errorf("memory.Store.Put(%s): store content limit %d bytes reached; delete or compact old memories before saving more", e.ID, MaxStoreContentBytes)
	}
	return nil
}

// Delete removes the entry from both the markdown file and the
// SQLite tables. Missing entries are not an error.
func (s *Store) Delete(id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	e, err := scanEntry(tx.QueryRow(`SELECT id, scope, file_path, line_start, line_end, content, tags, source, created_at, updated_at FROM memory_entries WHERE id = ?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_ = tx.Rollback()
			// A retry after a previously committed deletion may be the first
			// opportunity to finish its still-pending mirror cleanup.
			return s.drainMirrorOutboxLocked()
		}
		return err
	}
	if _, err := tx.Exec(`DELETE FROM memory_entries WHERE id = ?`, id); err != nil {
		return fmt.Errorf("memory.Store.Delete(%s): db: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM memory_fts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("memory.Store.Delete(%s): fts: %w", id, err)
	}
	if err := enqueueMirrorTx(tx, e.Scope); err != nil {
		return fmt.Errorf("memory.Store.Delete(%s): enqueue mirror: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.afterDelete(id)
	if err := s.drainMirrorOutboxLocked(); err != nil {
		return fmt.Errorf("memory.Store.Delete(%s): %w", id, err)
	}
	return nil
}

// Clear removes all learned state in one SQLite transaction and queues every
// affected scope for mirror cleanup. A filesystem failure therefore cannot
// roll back into a half-cleared database or become permanent.
func (s *Store) Clear() (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	entries, err := s.List("", 0)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	scopes := make(map[string]struct{})
	for _, entry := range entries {
		scopes[entry.Scope] = struct{}{}
	}
	for scope := range scopes {
		if err := enqueueMirrorTx(tx, scope); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(`DELETE FROM memory_entries`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM memory_fts`); err != nil {
		return 0, err
	}
	for _, table := range []string{"memory_scratch_archive", "memory_vectors", "project_cards"} {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&exists); err != nil {
			return 0, err
		}
		if exists > 0 {
			if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
				return 0, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	for _, entry := range entries {
		s.afterDelete(entry.ID)
	}
	if err := s.drainMirrorOutboxLocked(); err != nil {
		return len(entries), fmt.Errorf("memory.Store.Clear: %w", err)
	}
	if err := s.removeStaleMarkdownLocked(); err != nil {
		return len(entries), fmt.Errorf("memory.Store.Clear: %w", err)
	}
	return len(entries), nil
}

// List returns entries for a scope, newest first. Empty scope
// returns everything. Limit <= 0 means no limit.
