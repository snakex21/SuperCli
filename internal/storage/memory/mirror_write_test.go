package memory

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMirrorUpdatesOnlyChangedPositions(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i, id := range []string{"a", "b", "c"} {
		if err := s.Put(Entry{ID: id, Scope: ScopeFact, Content: "line", CreatedAt: time.Unix(int64(i+1), 0)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`CREATE TABLE position_updates(id TEXT); CREATE TRIGGER audit_positions AFTER UPDATE OF file_path,line_start,line_end ON memory_entries BEGIN INSERT INTO position_updates(id) VALUES(new.id); END`); err != nil {
		t.Fatal(err)
	}
	assertPositions := func(want []string) {
		t.Helper()
		rows, err := s.db.Query(`SELECT id FROM position_updates ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			got = append(got, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("position writes=%v want=%v", got, want)
		}
		p, _, _ := ScopeFile(s.markdownRoot(), ScopeFact)
		parsed, err := mdRead(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, position := range parsed {
			stored, err := s.Get(position.ID)
			if err != nil || stored.FilePath != p || stored.LineStart != position.LineStart || stored.LineEnd != position.LineEnd {
				t.Fatalf("wrong position: %+v vs %+v, %v", stored, position, err)
			}
		}
		if _, err := s.db.Exec(`DELETE FROM position_updates`); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.reconcileMarkdown(); err != nil {
		t.Fatal(err)
	}
	assertPositions([]string{})
	if err := s.Put(Entry{ID: "c", Scope: ScopeFact, Content: "updated", CreatedAt: time.Unix(3, 0)}); err != nil {
		t.Fatal(err)
	}
	assertPositions([]string{"c"})
	if err := s.Put(Entry{ID: "a", Scope: ScopeFact, Content: "first\nsecond\nthird", CreatedAt: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	assertPositions([]string{"a", "b", "c"})
	if err := s.Delete("b"); err != nil {
		t.Fatal(err)
	}
	assertPositions([]string{"c"})
	if _, err := s.db.Exec(`UPDATE memory_entries SET file_path='old-folder' WHERE id='a'; DELETE FROM position_updates`); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileMarkdown(); err != nil {
		t.Fatal(err)
	}
	assertPositions([]string{"a"})
}
func TestMarkdownMultilineParserPreservesWhitespaceAndLinePositions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "general.md")
	lines := []string{"# general memory", "", "## a (2026-09-30T10:00:00Z, user)", "", "α", "\t", "", "## b (2026-09-30T10:00:00Z, tool)", "", "", "β", "", ""}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\r\n")+"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := mdRead(p)
	if err != nil || len(entries) != 2 {
		t.Fatalf("parse=%+v %v", entries, err)
	}
	if entries[0].Content != "α\n\t\n" || entries[0].LineStart != 3 || entries[0].LineEnd != 7 || entries[1].Content != "β" || entries[1].LineStart != 8 || entries[1].LineEnd != 13 {
		t.Fatalf("format changed: %+v", entries)
	}
}
