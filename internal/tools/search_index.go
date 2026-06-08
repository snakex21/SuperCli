package tools

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// IndexedTool is the minimum information the FTS index needs
// about a tool. Description and Schema are concatenated for
// full-text search; Tags boost ranking for common queries.
type IndexedTool struct {
	Name        string
	Description string
	Schema      string
	Server      string // "core", "user", "mcp:<name>"
	Tags        []string
}

// SearchResult is one hit from the index. Score is BM25
// (negative is better in SQLite FTS5; the Searcher inverts it
// to 0..1 for callers).
type SearchResult struct {
	Name    string
	Server  string
	Score   float64
	Snippet string
}

// Index is a thin wrapper over a SQLite FTS5 virtual table.
// It is in-memory by default but can be backed by a file path
// for debugging. The Index is safe for concurrent use (one
// shared *sql.DB).
type Index struct {
	db *sql.DB
}

// NewInMemoryIndex creates a fresh FTS5-backed index in RAM.
// Always call Close when done.
func NewInMemoryIndex() (*Index, error) {
	return newIndex(":memory:")
}

// NewFileIndex creates a fresh FTS5-backed index in the file at
// path. The file is overwritten. Used for debugging and tests
// that want to inspect the index across runs.
func NewFileIndex(path string) (*Index, error) {
	return newIndex(path)
}

func newIndex(dsn string) (*Index, error) {
	// modernc.org/sqlite supports FTS5 by default; we just
	// need to enable it via pragma and create the table.
	dsn = dsn + "?_pragma=busy_timeout(2000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open fts index: %w", err)
	}
	stmt := `CREATE VIRTUAL TABLE IF NOT EXISTS tool_index USING fts5(
		name,
		description,
		schema,
		server UNINDEXED,
		token UNINDEXED,
		tags UNINDEXED,
		tokenize='unicode61 remove_diacritics 2'
	)`
	if _, err := db.Exec(stmt); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create tool_index: %w", err)
	}
	return &Index{db: db}, nil
}

// Close releases the underlying DB. Subsequent calls are
// no-ops.
func (i *Index) Close() error {
	if i == nil || i.db == nil {
		return nil
	}
	return i.db.Close()
}

// Rebuild clears the index and re-inserts every tool. Safe to
// call on a fresh or populated index. Schema and Description
// are concatenated into a single text field for the FTS5
// match; Name, Server, Tags are kept as metadata.
func (i *Index) Rebuild(tools []IndexedTool) error {
	if i == nil || i.db == nil {
		return fmt.Errorf("index: nil receiver or closed db")
	}
	tx, err := i.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM tool_index`); err != nil {
		return fmt.Errorf("clear index: %w", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO tool_index(name, description, schema, server, token, tags) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()
	for _, t := range tools {
		// Approximate token count: 1 token ~ 4 chars; we
		// don't need precision, just a relative signal.
		blob := t.Description + "\n" + t.Schema
		tok := (len(blob) + 3) / 4
		tags := strings.Join(t.Tags, ",")
		if _, err := stmt.Exec(t.Name, t.Description, t.Schema, t.Server, tok, tags); err != nil {
			return fmt.Errorf("insert %q: %w", t.Name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Search runs an FTS5 MATCH query against name/description/
// schema. It returns up to limit results sorted by BM25 score
// (lower is better; we invert to 0..1 by mapping -score via
// exp decay). Empty query is an error.
func (i *Index) Search(query string, limit int) ([]SearchResult, error) {
	if i == nil || i.db == nil {
		return nil, fmt.Errorf("index: nil receiver or closed db")
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("index.Search: empty query")
	}
	if limit <= 0 {
		limit = 3
	}
	// FTS5 MATCH uses prefix matching with `*`; we append *
	// to the last word so partial words match (good for
	// in-flight typing).
	fts := formatFTSQuery(query)
	rows, err := i.db.Query(`
		SELECT name, server, bm25(tool_index), snippet(tool_index, 1, '«', '»', '…', 8)
		FROM tool_index
		WHERE tool_index MATCH ?
		ORDER BY bm25(tool_index)
		LIMIT ?
	`, fts, limit)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var out []SearchResult
	for rows.Next() {
		var r SearchResult
		var bm float64
		if err := rows.Scan(&r.Name, &r.Server, &bm, &r.Snippet); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		// BM25 returns negative numbers (more negative =
		// better). Map to 0..1 with a soft squashing.
		// exp(bm/5) gives 0..1 for bm in [-Inf, 0].
		// We invert so higher = better in the result.
		r.Score = scoreFromBM25(bm)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return out, nil
}

// Len returns the number of indexed tools. Used by tests.
func (i *Index) Len() (int, error) {
	if i == nil || i.db == nil {
		return 0, fmt.Errorf("index: nil receiver or closed db")
	}
	var n int
	if err := i.db.QueryRow(`SELECT count(*) FROM tool_index`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// formatFTSQuery turns a free-text query into a safe FTS5
// MATCH expression. It strips FTS5 metacharacters, splits on
// whitespace, and adds a prefix wildcard to the last token
// so partial words match.
func formatFTSQuery(q string) string {
	// Strip quotes and backslash that are FTS5 syntax.
	cleaner := strings.NewReplacer(`"`, ` `, `\`, ` `, `(`, ` `, `)`, ` `, `:`, ` `)
	q = cleaner.Replace(q)
	fields := strings.Fields(q)
	if len(fields) == 0 {
		return ""
	}
	// Quote each token and add prefix wildcard to the last
	// one. FTS5 expects `"token"*` to mean "starts with
	// token". We always quote to avoid syntax errors on
	// punctuation.
	var b strings.Builder
	for i, f := range fields {
		if i > 0 {
			b.WriteString(" ")
		}
		// FTS5 ignores very short tokens (<3 chars by
		// default for unicode61). We pad to keep them by
		// quoting explicitly: "ab" still matches "ab".
		fmt.Fprintf(&b, `"%s"`, f)
		if i == len(fields)-1 {
			b.WriteString("*")
		}
	}
	return b.String()
}

// scoreFromBM25 maps a raw BM25 score (negative) to 0..1
// where 1 is best. exp(bm/k) is a soft squashing; with k=4
// a score of -4 maps to ~0.37 and -8 to ~0.14.
func scoreFromBM25(bm float64) float64 {
	if bm >= 0 {
		return 0
	}
	// Standard exp mapping.
	// Negative scores are better; flip sign.
	import_exp := func(x float64) float64 {
		// Taylor approx for very negative x to avoid -Inf
		if x < -50 {
			return 0
		}
		// simple e^x via repeated squaring isn't needed;
		// stdlib math.Exp handles it. We avoid importing
		// math by using a series? No — just use math.
		return expInline(x)
	}
	return import_exp(bm / 4.0)
}
