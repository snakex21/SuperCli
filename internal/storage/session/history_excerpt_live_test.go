package session

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Explicit offline opt-in: open the existing database with SQLite mode=ro,
// bypass OpenStore (migrations/media recovery/housekeeping are forbidden here).
func TestHistoryExcerptReadOnlyLongSession(t *testing.T) {
	path, id := os.Getenv("SUPERCLI_LONG_SESSION_DB"), os.Getenv("SUPERCLI_LONG_SESSION_ID")
	if path == "" || id == "" {
		t.Skip("opt-in local read-only lookup measurement")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(abs)
	if err != nil || !st.Mode().IsRegular() {
		t.Fatal("existing database required")
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var guarded int
	if err = db.QueryRowContext(ctx, "PRAGMA query_only").Scan(&guarded); err != nil || guarded != 1 {
		t.Fatal("read-only guard required")
	}
	var seq, rows int
	if err = db.QueryRowContext(ctx, "SELECT max(seq),count(*) FROM messages WHERE session_id=?", id).Scan(&seq, &rows); err != nil {
		t.Fatal(err)
	}
	store := &Store{db: db}
	page, err := store.ReadHistoryExcerpt(ctx, id, seq, 0, 8192)
	if err != nil {
		t.Fatal(err)
	}
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := store.ReadHistoryExcerpt(ctx, id, seq, 0, 8192); err != nil {
				b.Fatal(err)
			}
		}
	})
	started := time.Now()
	hits, err := store.SearchHistory(ctx, "checkpoint", id, "", time.Time{}, time.Time{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("READ ONLY: archive_rows=%d selected_seq=%d excerpt_bytes=%d total_message_bytes=%d lookup=%.3f ms/op %d B/op; indexed_search_hits=%d search_ms=%.3f; no transcript text exported", rows, seq, len(page.Text), page.TotalBytes, float64(result.NsPerOp())/1e6, result.AllocedBytesPerOp(), len(hits), float64(time.Since(started).Microseconds())/1000)
}
