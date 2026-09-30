package memory

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedMemoryWriteFixture(b testing.TB, count int, content string) *Store {
	b.Helper()
	s, err := OpenStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	tx, err := s.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO memory_entries(id,scope,file_path,content,created_at,updated_at) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		b.Fatal(err)
	}
	defer stmt.Close()
	for i := 0; i < count; i++ {
		if _, err := stmt.Exec(fmt.Sprintf("note-%05d", i), ScopeFact, "", content, 1700000000, 1700000000); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if err := s.reconcileMarkdown(); err != nil {
		b.Fatal(err)
	}
	return s
}
func BenchmarkMemoryCapacityCheck(b *testing.B) {
	s := seedMemoryWriteFixture(b, 4000, strings.Repeat("persisted fact ", 140))
	entry := Entry{ID: "note-03999", Scope: ScopeFact, Content: "updated fact"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := checkMemoryCapacity(s.db, entry); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkMemoryPutMirror(b *testing.B) {
	s := seedMemoryWriteFixture(b, 1000, "short note")
	entry := Entry{ID: "note-00999", Scope: ScopeFact, CreatedAt: time.Unix(1700000000, 0)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		entry.Content = fmt.Sprintf("updated note %d", i)
		if err := s.Put(entry); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkMemoryMarkdownMultiline(b *testing.B) {
	p := filepath.Join(b.TempDir(), "general.md")
	content := strings.Repeat("file.go: context and completed work for this task\n", 300)
	if err := mdWrite(p, []Entry{{ID: "note", Content: content, Source: SourceAgent, CreatedAt: time.Unix(1700000000, 0)}}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		entries, err := mdRead(p)
		if err != nil || len(entries) != 1 {
			b.Fatal(entries, err)
		}
	}
}
