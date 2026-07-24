package memory

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var processMirrorLocks sync.Map

func processMirrorLock(root string) *sync.Mutex {
	key := canonicalPath(filepath.Join(root, "memory.db"))
	lock, _ := processMirrorLocks.LoadOrStore(key, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// enqueueMirrorTx records mirror work in the same SQLite transaction as the
// authoritative mutation. Incrementing generation prevents a renderer from
// acknowledging work that was superseded while it was writing the file.
func enqueueMirrorTx(tx *sql.Tx, scope string) error {
	if strings.TrimSpace(scope) == "" {
		return nil
	}
	_, err := tx.Exec(`
		INSERT INTO memory_mirror_outbox(scope, generation, enqueued_at)
		VALUES (?, 1, ?)
		ON CONFLICT(scope) DO UPDATE SET
			generation = memory_mirror_outbox.generation + 1,
			enqueued_at = excluded.enqueued_at`, scope, time.Now().UnixNano())
	return err
}

// reconcileMarkdown rebuilds the human-readable mirror from SQLite at
// startup. SQLite and the durable outbox are the source of truth; a crash or a
// failed filesystem operation can therefore delay a mirror, but cannot make a
// partial Markdown write authoritative.
func (s *Store) reconcileMarkdown() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.reconcileMarkdownLocked()
}

func (s *Store) reconcileMarkdownLocked() error {
	rows, err := s.db.Query(`SELECT DISTINCT scope FROM memory_entries ORDER BY scope`)
	if err != nil {
		return fmt.Errorf("memory mirror: list scopes: %w", err)
	}
	var scopes []string
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			rows.Close()
			return err
		}
		scopes = append(scopes, scope)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, scope := range scopes {
		if err := enqueueMirrorTx(tx, scope); err != nil {
			return fmt.Errorf("memory mirror: enqueue %s: %w", scope, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := s.drainMirrorOutboxLocked(); err != nil {
		return err
	}
	return s.removeStaleMarkdownLocked()
}

func (s *Store) drainMirrorOutboxLocked() error {
	// writeMu is per Store, while WebGUI may open multiple Store values for the
	// same database. The process lock avoids needless local SQLite contention;
	// renderNextMirrorLocked additionally holds an IMMEDIATE SQLite transaction
	// across selection, snapshot, replace and acknowledgement, which provides the
	// same ordering guarantee between separate SuperCli processes.
	s.mirrorMu.Lock()
	defer s.mirrorMu.Unlock()
	for {
		empty, err := s.renderNextMirrorLocked()
		if err != nil {
			return err
		}
		if empty {
			return nil
		}
	}
}

func (s *Store) renderNextMirrorLocked() (empty bool, err error) {
	// OpenStore configures transactions as IMMEDIATE. Beginning before reading
	// the outbox is essential: selecting generation 1 outside this transaction
	// would still allow another process to render and acknowledge generation 2
	// before this process replaced the file with its stale snapshot.
	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("memory mirror: begin render: %w", err)
	}
	defer tx.Rollback()

	var scope string
	var generation int64
	err = tx.QueryRow(`
		SELECT scope, generation FROM memory_mirror_outbox
		ORDER BY enqueued_at, scope LIMIT 1`).Scan(&scope, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("memory mirror: read outbox: %w", err)
	}
	if s.beforeMirrorWrite != nil {
		if err := s.beforeMirrorWrite(scope); err != nil {
			return false, fmt.Errorf("memory mirror %s: %w", scope, err)
		}
	}
	if err := s.renderScopeMirrorLocked(tx, scope, generation); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("memory mirror %s: commit: %w", scope, err)
	}
	return false, nil
}

func (s *Store) renderScopeMirrorLocked(tx *sql.Tx, scope string, generation int64) error {
	path, err := s.mirrorPathForScopeQuery(tx, scope)
	if err != nil {
		return fmt.Errorf("memory mirror %s: path: %w", scope, err)
	}
	rows, err := tx.Query(`
		SELECT id, scope, file_path, line_start, line_end, content, tags, source, created_at, updated_at
		FROM memory_entries WHERE scope = ?
		ORDER BY created_at, id`, scope)
	if err != nil {
		return fmt.Errorf("memory mirror %s: list entries: %w", scope, err)
	}
	entries, err := scanAll(rows, 0)
	rows.Close()
	if err != nil {
		return fmt.Errorf("memory mirror %s: scan entries: %w", scope, err)
	}
	for i := range entries {
		entries[i].FilePath = path
	}
	if s.afterMirrorRead != nil {
		s.afterMirrorRead(scope)
	}
	// This check belongs immediately before the filesystem side effect. The
	// IMMEDIATE transaction prevents it changing until commit, including when a
	// second renderer is running in another process.
	var current int64
	if err := tx.QueryRow(`SELECT generation FROM memory_mirror_outbox WHERE scope = ?`, scope).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if current != generation {
		return nil
	}

	var positions []Entry
	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("memory mirror %s: remove %s: %w", scope, path, err)
		}
		if err := syncDirectory(filepath.Dir(path)); err != nil {
			return fmt.Errorf("memory mirror %s: sync directory: %w", scope, err)
		}
	} else {
		if err := mdWrite(path, entries); err != nil {
			return fmt.Errorf("memory mirror %s: write: %w", scope, err)
		}
		positions, err = mdRead(path)
		if err != nil {
			return fmt.Errorf("memory mirror %s: verify: %w", scope, err)
		}
	}

	for _, entry := range positions {
		if _, err := tx.Exec(`
			UPDATE memory_entries SET file_path = ?, line_start = ?, line_end = ?
			WHERE id = ? AND scope = ?`, path, entry.LineStart, entry.LineEnd, entry.ID, scope); err != nil {
			return fmt.Errorf("memory mirror %s: update position %s: %w", scope, entry.ID, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM memory_mirror_outbox WHERE scope = ? AND generation = ?`, scope, generation); err != nil {
		return err
	}
	return nil
}

func (s *Store) mirrorPathForScope(scope string) (string, error) {
	return s.mirrorPathForScopeQuery(s.db, scope)
}

type mirrorRowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func (s *Store) mirrorPathForScopeQuery(query mirrorRowQuerier, scope string) (string, error) {
	path, _, err := ScopeFile(s.markdownRoot(), scope)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(scope, "scratch:") {
		date := strings.TrimPrefix(scope, "scratch:")
		var archived string
		if err := query.QueryRow(`SELECT path FROM memory_scratch_archive WHERE date = ?`, date).Scan(&archived); err == nil {
			// Never trust a persisted/imported absolute path. Archive mirrors are
			// always reconstructed below this store's own memory directory.
			path = filepath.Join(s.markdownRoot(), "archive", filepath.Base(archived))
		} else if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	return filepath.Clean(path), nil
}

func (s *Store) removeStaleMarkdownLocked() error {
	// Keep the expected-set snapshot and filesystem deletion under the same
	// cross-process ordering used by rendering. Otherwise another process can
	// create and acknowledge a new scope after this function reads expected but
	// before WalkDir, leaving its newly created mirror deleted with no outbox
	// work remaining to restore it.
	s.mirrorMu.Lock()
	defer s.mirrorMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("memory mirror: begin stale cleanup: %w", err)
	}
	defer tx.Rollback()

	expected := make(map[string]struct{})
	rows, err := tx.Query(`SELECT DISTINCT scope FROM memory_entries`)
	if err != nil {
		return err
	}
	var scopes []string
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			rows.Close()
			return err
		}
		scopes = append(scopes, scope)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, scope := range scopes {
		path, err := s.mirrorPathForScopeQuery(tx, scope)
		if err != nil {
			return err
		}
		expected[canonicalPath(path)] = struct{}{}
	}
	if s.afterStaleRead != nil {
		s.afterStaleRead()
	}

	err = filepath.WalkDir(s.markdownRoot(), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		if _, ok := expected[canonicalPath(path)]; ok {
			return nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("memory mirror: remove stale files: %w", err)
	}
	for _, sub := range []string{"", "patterns", "archive"} {
		if err := mkdirAll(filepath.Join(s.markdownRoot(), sub), 0o755); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory mirror: commit stale cleanup: %w", err)
	}
	return nil
}

func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	return strings.ToLower(filepath.Clean(abs))
}
