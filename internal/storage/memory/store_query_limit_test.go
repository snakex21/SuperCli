package memory

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// Seed the read benchmark in one transaction. Mirror/embedding writes are not
// part of the measured operation; use the real Store schema and query methods.
func queryLimitStore(t testing.TB, entries []Entry) *Store {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO memory_entries(id,scope,file_path,line_start,line_end,content,tags,source,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err := stmt.Exec(e.ID, e.Scope, e.FilePath, e.LineStart, e.LineEnd, e.Content, strings.Join(e.Tags, ","), e.Source, e.CreatedAt.Unix(), e.UpdatedAt.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return store
}

func queryLimitEntries(count int) []Entry {
	entries := make([]Entry, 0, count)
	scopes := []string{ScopeFact, ScopeTaskLog, ScopeDecision, ScopePreference}
	for i := 0; i < count; i++ {
		entries = append(entries, Entry{
			ID: fmt.Sprintf("entry-%04d", i), Scope: scopes[i%len(scopes)], FilePath: "source.md", LineStart: i + 1, LineEnd: i + 2,
			Content: fmt.Sprintf("Entry %d: ", i) + strings.Repeat("Remember the portable project decision. ", 24),
			Tags:    []string{"portable", "fixture"}, Source: SourceAgent,
			CreatedAt: time.Unix(int64(1_700_000_000+i/3), 0).UTC(),
			UpdatedAt: time.Unix(int64(1_700_010_000+(count-i)/5), 0).UTC(),
		})
	}
	return entries
}

func expectedRecentEntries(entries []Entry, scope string, limit int, created bool) []Entry {
	var wanted []Entry
	for _, e := range entries {
		if scope == "" || e.Scope == scope {
			wanted = append(wanted, e)
		}
	}
	sort.Slice(wanted, func(i, j int) bool {
		a, b := wanted[i], wanted[j]
		if !created && !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.After(b.CreatedAt)
		}
		return a.ID < b.ID
	})
	if limit > 0 && limit < len(wanted) {
		wanted = wanted[:limit]
	}
	return wanted
}

func TestMemoryQueryLimitsPreserveEntriesAndOrdering(t *testing.T) {
	entries := queryLimitEntries(67)
	store := queryLimitStore(t, entries)
	for _, scope := range []string{"", ScopeFact, ScopeTaskLog, "missing", "fact' OR 1=1 --"} {
		for _, limit := range []int{-2, 0, 1, 3, 50, 100} {
			for _, kind := range []string{"list", "recent", "created"} {
				var got []Entry
				var err error
				switch kind {
				case "list":
					got, err = store.List(scope, limit)
				case "recent":
					got, err = store.Recent(scope, limit)
				case "created":
					got, err = store.recentByCreated(scope, limit)
				}
				if err != nil {
					t.Fatal(err)
				}
				want := expectedRecentEntries(entries, scope, limit, kind == "created")
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s scope=%q limit=%d returned a different ordered result: got=%d want=%d", kind, scope, limit, len(got), len(want))
				}
			}
		}
	}
	// A committed update must be visible on the next limited read.
	newer := Entry{ID: "new", Scope: ScopeFact, Content: "newest visible fact", Source: SourceAgent}
	if err := store.Put(newer); err != nil {
		t.Fatal(err)
	}
	got, err := store.Recent(ScopeFact, 1)
	if err != nil || len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("newest write not visible: %v", err)
	}
}

func BenchmarkMemoryQueryLimits(b *testing.B) {
	for _, count := range []int{64, 4096} {
		store := queryLimitStore(b, queryLimitEntries(count))
		for _, operation := range []string{"all-recent", "scoped-recent", "created-budget", "briefing"} {
			b.Run(fmt.Sprintf("%d/%s", count, operation), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					switch operation {
					case "all-recent":
						entries, err := store.Recent("", 6)
						if err != nil || len(entries) != 6 {
							b.Fatalf("recent: %v", err)
						}
					case "scoped-recent":
						entries, err := store.Recent(ScopeTaskLog, 3)
						if err != nil || len(entries) != 3 {
							b.Fatalf("scoped recent: %v", err)
						}
					case "created-budget":
						result, err := store.RecentBudgeted("", 700)
						if err != nil || result == "" {
							b.Fatalf("budgeted: %v", err)
						}
					case "briefing":
						if BuildBriefing(nil, store, "fixture-project", 700) == "" {
							b.Fatal("empty briefing")
						}
					}
				}
			})
		}
	}
}

type memoryQuerySnapshot struct {
	Name    string
	Entries []struct {
		ID, Scope, Content, Tags, Source string
		Created                          int64 `json:"created_at"`
		Updated                          int64 `json:"updated_at"`
	}
}

func loadMemoryQuerySnapshots(t testing.TB) []memoryQuerySnapshot {
	t.Helper()
	path := os.Getenv("SUPERCLI_MEMORY_QUERY_SNAPSHOT")
	if path == "" {
		t.Skip("set SUPERCLI_MEMORY_QUERY_SNAPSHOT to a local memory snapshot")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []memoryQuerySnapshot
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("empty snapshot")
	}
	return fixtures
}
func querySnapshotEntries(fixture memoryQuerySnapshot) []Entry {
	entries := make([]Entry, 0, len(fixture.Entries))
	for _, e := range fixture.Entries {
		entries = append(entries, Entry{ID: e.ID, Scope: e.Scope, Content: e.Content, Tags: EntriesFromCSV(e.Tags), Source: e.Source, CreatedAt: time.Unix(e.Created, 0).UTC(), UpdatedAt: time.Unix(e.Updated, 0).UTC()})
	}
	return entries
}

func TestMemoryQuerySavedSnapshot(t *testing.T) {
	for _, fixture := range loadMemoryQuerySnapshots(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			entries := querySnapshotEntries(fixture)
			store := queryLimitStore(t, entries)
			var fingerprints strings.Builder
			for _, scope := range []string{"", ScopeFact, ScopeTaskLog, ScopeDecision, ScopeRawLog} {
				for _, limit := range []int{0, 1, 3, 6, 50} {
					for _, created := range []bool{false, true} {
						var got []Entry
						var err error
						if created {
							got, err = store.recentByCreated(scope, limit)
						} else {
							got, err = store.Recent(scope, limit)
						}
						if err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(got, expectedRecentEntries(entries, scope, limit, created)) {
							t.Fatalf("snapshot ordering/content changed: scope=%q limit=%d", scope, limit)
						}
						encoded, _ := json.Marshal(got)
						fingerprints.Write(encoded)
					}
				}
				for _, cap := range []int{120, 420, 700} {
					got, err := store.RecentBudgeted(scope, cap)
					if err != nil {
						t.Fatal(err)
					}
					fingerprints.WriteString(got)
				}
			}
			for _, cap := range []int{120, 420, 700} {
				fingerprints.WriteString(BuildBriefing(nil, store, "fixture-project", cap))
			}
			t.Logf("rows=%d output_fingerprint=%x", len(entries), sha256.Sum256([]byte(fingerprints.String())))
		})
	}
}

func BenchmarkMemoryQuerySavedSnapshot(b *testing.B) {
	for _, fixture := range loadMemoryQuerySnapshots(b) {
		store := queryLimitStore(b, querySnapshotEntries(fixture))
		b.Run(fixture.Name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if BuildBriefing(nil, store, "fixture-project", 700) == "" {
					b.Fatal("empty briefing")
				}
			}
		})
	}
}

func BenchmarkMemoryQueryStorageCost(b *testing.B) {
	for _, count := range []int{64, 4096} {
		entries := queryLimitEntries(count)
		store := queryLimitStore(b, entries)
		// Reconcile mirrors before timing reopen; initial imports are a separate cost.
		warmed, err := OpenStore(store.Root())
		if err != nil {
			b.Fatal(err)
		}
		if err := warmed.Close(); err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("%d/reopen", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				other, err := OpenStore(store.Root())
				if err != nil {
					b.Fatal(err)
				}
				if err := other.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("%d/update", count), func(b *testing.B) {
			b.ReportAllocs()
			entry := entries[0]
			for i := 0; i < b.N; i++ {
				entry.Content = fmt.Sprintf("revision-%d ", i) + entries[0].Content
				if err := store.Put(entry); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestMemoryQueryStorageShape(t *testing.T) {
	store := queryLimitStore(t, queryLimitEntries(4096))
	var pages, size int64
	if err := store.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("PRAGMA page_size").Scan(&size); err != nil {
		t.Fatal(err)
	}
	t.Logf("logical database bytes with 4096 fixture entries: %d", pages*size)
	for _, query := range []string{
		"SELECT id,content FROM memory_entries ORDER BY updated_at DESC,created_at DESC,id LIMIT 6",
		"SELECT id,content FROM memory_entries WHERE scope='task-log' ORDER BY updated_at DESC,created_at DESC,id LIMIT 3",
		"SELECT id,content FROM memory_entries ORDER BY created_at DESC,id LIMIT 50",
		"SELECT id,content FROM memory_entries WHERE scope='task-log' ORDER BY created_at DESC,id LIMIT 50",
	} {
		rows, err := store.db.Query("EXPLAIN QUERY PLAN " + query)
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("query=%s plan=%s", query, strings.Join(details, "; "))
	}
}

func TestMemoryRecencyIndexUpgradePreservesSavedEntries(t *testing.T) {
	root := t.TempDir()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i := 0; i < 5; i++ {
		if err := store.Put(Entry{ID: fmt.Sprintf("saved-%d", i), Scope: ScopeFact, Content: fmt.Sprintf("portable migration evidence %d", i), Source: SourceAgent}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.List("", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the old index layout without altering any application data.
	for _, q := range []string{
		"DROP INDEX idx_memory_recent", "DROP INDEX idx_memory_scope_recent",
		"DROP INDEX idx_memory_created", "DROP INDEX idx_memory_scope_created",
		"CREATE INDEX idx_memory_scope ON memory_entries(scope)",
		"CREATE INDEX idx_memory_updated ON memory_entries(updated_at DESC)",
	} {
		if _, err := store.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, err := reopened.List("", 0)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("migration changed entries: %v", err)
	}
	hits, err := reopened.Search("migration", 10)
	if err != nil || len(hits) != 5 {
		t.Fatalf("migration lost FTS evidence: count=%d err=%v", len(hits), err)
	}
	if err := reopened.Put(Entry{ID: "saved-0", Scope: ScopeFact, Content: "updated migration evidence", Source: SourceAgent}); err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get("saved-0")
	if err != nil || got.Content != "updated migration evidence" {
		t.Fatalf("write after migration failed: %v", err)
	}
	if err := reopened.Delete("saved-1"); err != nil {
		t.Fatal(err)
	}
	hits, err = reopened.Search("migration", 10)
	if err != nil || len(hits) != 4 {
		t.Fatalf("delete after migration failed: count=%d err=%v", len(hits), err)
	}
}
