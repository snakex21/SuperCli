package memory

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

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
	entries, err := s.recentByCreated(scope, 50)
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

// recentByCreated returns the newest entries in scope ordered by
// CREATION time (created_at DESC, id as a stable tie-break). Unlike
// List/Recent — which order by updated_at first — this is immune to
// the millisecond race where three quick Puts get slightly different
// updated_at values and reorder unpredictably. RecentBudgeted needs
// "newest authored", and a deterministic order, so it uses this.
func (s *Store) recentByCreated(scope string, limit int) ([]Entry, error) {
	var rows *sql.Rows
	var err error
	if scope == "" {
		rows, err = s.db.Query(`SELECT id, scope, file_path, line_start, line_end, content, tags, source, created_at, updated_at FROM memory_entries ORDER BY created_at DESC, id`)
	} else {
		rows, err = s.db.Query(`SELECT id, scope, file_path, line_start, line_end, content, tags, source, created_at, updated_at FROM memory_entries WHERE scope = ? ORDER BY created_at DESC, id`, scope)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAll(rows, limit)
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
	// Make room before writing so the store-wide capacity guard cannot block a
	// fresh scratch note merely because today's automatic scope reached its cap.
	_, _ = s.Retain(scope, MaxDailyScratchEntries-1)
	if err := s.Put(Entry{
		ID:      id,
		Scope:   scope,
		Content: line,
		Source:  SourceAgent,
	}); err != nil {
		return err
	}
	_, err := s.Retain(scope, MaxDailyScratchEntries)
	return err
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
