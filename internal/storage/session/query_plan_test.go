package session

import (
	"strings"
	"testing"
)

func TestSessionListIndexesAvoidTempSort(t *testing.T) {
	s := openTestStore(t)
	for i := 0; i < 8; i++ {
		cwd := "/a"
		if i%2 != 0 {
			cwd = "/b"
		}
		if _, err := s.Create(cwd, "model", "session"); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	assertQueryPlanHasNoTempSort(t, s,
		`SELECT id FROM sessions ORDER BY updated_at DESC, created_at DESC, id DESC LIMIT ?`, 5)
	assertQueryPlanHasNoTempSort(t, s,
		`SELECT id FROM sessions WHERE cwd = ? ORDER BY updated_at DESC, created_at DESC, id DESC LIMIT ?`, "/a", 5)
	assertQueryPlanHasNoTempSort(t, s,
		`SELECT seq FROM messages WHERE session_id = ? AND seq < ? ORDER BY seq DESC LIMIT ?`, "session", 100, 20)
}

func assertQueryPlanHasNoTempSort(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan = append(plan, detail)
		if strings.Contains(strings.ToUpper(detail), "TEMP B-TREE") {
			t.Fatalf("query still uses a temporary sort: %s", strings.Join(plan, "; "))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("query plan rows: %v", err)
	}
}
