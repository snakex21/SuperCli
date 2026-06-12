package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the persistent memory store: SQLite for queries
// (with FTS5 search) plus a markdown file mirror for human
// inspection. The two are kept in sync by every Put/Delete.
//
// A Store is safe for concurrent use. Close releases the
// underlying *sql.DB.
type Store struct {
	db   *sql.DB
	root string

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
	dsn := filepath.Join(home, "memory.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("memory.OpenStore: open: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("memory.OpenStore: ping: %w", err)
	}
	s := &Store{db: db, root: home}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("memory.OpenStore: migrate: %w", err)
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

// Put inserts or updates an entry. The markdown file mirror is
// kept in sync; the SQLite row stores the resulting line range.
func (s *Store) Put(e Entry) error {
	if err := e.Validate(); err != nil {
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
	if err := mdUpsert(filePath, e); err != nil {
		return fmt.Errorf("memory.Store.Put(%s): %w", e.ID, err)
	}
	// Re-read the file to capture the (possibly new) line range.
	disk, err := mdRead(filePath)
	if err != nil {
		return fmt.Errorf("memory.Store.Put(%s): reread: %w", e.ID, err)
	}
	var lineStart, lineEnd int
	for _, d := range disk {
		if d.ID == e.ID {
			lineStart = d.LineStart
			lineEnd = d.LineEnd
			break
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM memory_entries WHERE id = ?`, e.ID); err != nil {
		return fmt.Errorf("delete old: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM memory_fts WHERE id = ?`, e.ID); err != nil {
		return fmt.Errorf("delete fts: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO memory_entries(id, scope, file_path, line_start, line_end, content, tags, source, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.Scope, filePath, lineStart, lineEnd, e.Content, e.TagsCSV(), e.Source, e.CreatedAt.Unix(), e.UpdatedAt.Unix(),
	); err != nil {
		return fmt.Errorf("insert entry: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO memory_fts(id, scope, content, tags) VALUES (?,?,?,?)`,
		e.ID, e.Scope, e.Content, strings.Join(e.Tags, " "),
	); err != nil {
		return fmt.Errorf("insert fts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Best-effort vector indexing (hybrid.go) — never fails Put.
	s.afterPut(e)
	return nil
}

// Delete removes the entry from both the markdown file and the
// SQLite tables. Missing entries are not an error.
func (s *Store) Delete(id string) error {
	e, err := s.Get(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if err := mdDelete(e.FilePath, id); err != nil {
		return fmt.Errorf("memory.Store.Delete(%s): md: %w", id, err)
	}
	if _, err := s.db.Exec(`DELETE FROM memory_entries WHERE id = ?`, id); err != nil {
		return fmt.Errorf("memory.Store.Delete(%s): db: %w", id, err)
	}
	if _, err := s.db.Exec(`DELETE FROM memory_fts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("memory.Store.Delete(%s): fts: %w", id, err)
	}
	s.afterDelete(id)
	return nil
}

// List returns entries for a scope, newest first. Empty scope
// returns everything. Limit <= 0 means no limit.
func (s *Store) List(scope string, limit int) ([]Entry, error) {
	var rows *sql.Rows
	var err error
	if scope == "" {
		rows, err = s.db.Query(`SELECT id, scope, file_path, line_start, line_end, content, tags, source, created_at, updated_at FROM memory_entries ORDER BY updated_at DESC, created_at DESC, id`)
	} else {
		rows, err = s.db.Query(`SELECT id, scope, file_path, line_start, line_end, content, tags, source, created_at, updated_at FROM memory_entries WHERE scope = ? ORDER BY updated_at DESC, created_at DESC, id`, scope)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAll(rows, limit)
}

// Search runs an FTS5 query, falling back to LIKE when the
// query is empty or contains no FTS5 operators. Results are
// ranked by FTS5 rank (best match first) and capped at k.
func (s *Store) Search(query string, k int) ([]Entry, error) {
	if query == "" {
		return nil, nil
	}
	// FTS5 wants the query as-is. Escape embedded quotes by
	// switching to a phrase search if needed.
	rows, err := s.db.Query(
		`SELECT e.id, e.scope, e.file_path, e.line_start, e.line_end, e.content, e.tags, e.source, e.created_at, e.updated_at
		 FROM memory_fts f
		 JOIN memory_entries e ON e.id = f.id
		 WHERE memory_fts MATCH ?
		 ORDER BY rank
		 LIMIT ?`,
		query, k,
	)
	if err != nil {
		// Fall back to LIKE for queries that are not valid FTS5.
		rows, err = s.db.Query(
			`SELECT id, scope, file_path, line_start, line_end, content, tags, source, created_at, updated_at FROM memory_entries WHERE content LIKE ? ORDER BY updated_at DESC LIMIT ?`,
			"%"+query+"%", k,
		)
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()
	return scanAll(rows, 0)
}

// Recent returns the n most recent entries in the scope.
// scope == "" means every scope.
func (s *Store) Recent(scope string, n int) ([]Entry, error) {
	return s.List(scope, n)
}

// RecentBudgeted returns the newest entries in scope whose
// total estimated tokens is at most tokenCap. The returned
// string is the concatenated, rendered form (the same data
// the model would see).
func (s *Store) RecentBudgeted(scope string, tokenCap int) (string, error) {
	if tokenCap <= 0 {
		return "", nil
	}
	entries, err := s.Recent(scope, 50)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	tokens := 0
	for _, e := range entries {
		t := estimateTokens(e.Content)
		if tokens+t > tokenCap {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("- [")
		b.WriteString(e.ID)
		b.WriteString("] ")
		b.WriteString(oneLine(e.Content))
		tokens += t
	}
	return b.String(), nil
}

// ByTag returns up to k entries that include the given tag.
func (s *Store) ByTag(tag string, k int) ([]Entry, error) {
	rows, err := s.db.Query(
		`SELECT id, scope, file_path, line_start, line_end, content, tags, source, created_at, updated_at
		 FROM memory_entries
		 WHERE tags LIKE ?
		 ORDER BY updated_at DESC
		 LIMIT ?`,
		"%"+tag+"%", k,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAll(rows, 0)
}

// AppendScratch adds a single line to today's scratch file. The
// line gets a fresh id and the "agent" source.
func (s *Store) AppendScratch(line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return fmt.Errorf("memory.Store.AppendScratch: line is empty")
	}
	date := time.Now().UTC().Format("2006-01-02")
	scope := "scratch:" + date
	// id = date-nanos short
	id := fmt.Sprintf("%s-%x", date, time.Now().UnixNano()&0xffff)
	return s.Put(Entry{
		ID:      id,
		Scope:   scope,
		Content: line,
		Source:  SourceAgent,
	})
}

// ArchiveOldScratches moves scratch files older than 30 days
// into `<root>/memory/archive/`. Files already archived are
// skipped. Returns the count of newly archived files.
func (s *Store) ArchiveOldScratches(ctx context.Context) (int, error) {
	memDir := filepath.Join(s.root, "memory")
	entries, err := readDir(memDir)
	if err != nil {
		return 0, fmt.Errorf("memory.Store.ArchiveOldScratches: %w", err)
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -30)
	archived := 0
	for _, name := range entries {
		if ctx.Err() != nil {
			return archived, ctx.Err()
		}
		if !strings.HasPrefix(name, "scratch-") || !strings.HasSuffix(name, ".md") {
			continue
		}
		dateStr := strings.TrimSuffix(strings.TrimPrefix(name, "scratch-"), ".md")
		d, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		if !d.Before(cutoff) {
			continue
		}
		var existed int
		err = s.db.QueryRow(`SELECT COUNT(*) FROM memory_scratch_archive WHERE date = ?`, dateStr).Scan(&existed)
		if err != nil {
			return archived, err
		}
		if existed > 0 {
			continue
		}
		src := filepath.Join(memDir, name)
		dst := filepath.Join(memDir, "archive", name)
		if err := renameFile(src, dst); err != nil {
			return archived, err
		}
		if _, err := s.db.Exec(
			`INSERT OR REPLACE INTO memory_scratch_archive(date, path, archived_at) VALUES (?,?,?)`,
			dateStr, dst, time.Now().Unix(),
		); err != nil {
			return archived, err
		}
		archived++
	}
	return archived, nil
}

// markdownRoot returns the directory under root where the
// markdown files live.
func (s *Store) markdownRoot() string {
	return filepath.Join(s.root, "memory")
}

// scanEntry reads one row into an Entry.
func scanEntry(row *sql.Row) (Entry, error) {
	var e Entry
	var tagsCSV string
	var tsC, tsU int64
	err := row.Scan(&e.ID, &e.Scope, &e.FilePath, &e.LineStart, &e.LineEnd, &e.Content, &tagsCSV, &e.Source, &tsC, &tsU)
	if err != nil {
		return Entry{}, err
	}
	e.Tags = EntriesFromCSV(tagsCSV)
	e.CreatedAt = time.Unix(tsC, 0).UTC()
	e.UpdatedAt = time.Unix(tsU, 0).UTC()
	return e, nil
}

// scanAll reads entries from rows. limit <= 0 means no limit.
func scanAll(rows *sql.Rows, limit int) ([]Entry, error) {
	var out []Entry
	for rows.Next() {
		var e Entry
		var tagsCSV string
		var tsC, tsU int64
		if err := rows.Scan(&e.ID, &e.Scope, &e.FilePath, &e.LineStart, &e.LineEnd, &e.Content, &tagsCSV, &e.Source, &tsC, &tsU); err != nil {
			return nil, err
		}
		e.Tags = EntriesFromCSV(tagsCSV)
		e.CreatedAt = time.Unix(tsC, 0).UTC()
		e.UpdatedAt = time.Unix(tsU, 0).UTC()
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// estimateTokens is the same crude len/4 estimate as
// context.Source. Kept here to avoid an import cycle.
func estimateTokens(s string) int { return (len(s) + 3) / 4 }

// oneLine returns s with newlines collapsed to spaces.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// firstLine returns the first line of s (used to format
// error messages without dumping the full SQL).
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
