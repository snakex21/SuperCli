package memory

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMD_ReadEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "general.md")
	entries, err := mdRead(path)
	if err != nil {
		t.Fatalf("mdRead: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %v, want empty", entries)
	}
}

func TestMD_UpsertCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "general.md")
	e := Entry{ID: "a1", Content: "hello", Source: SourceUser}
	if err := mdUpsert(path, e); err != nil {
		t.Fatalf("mdUpsert: %v", err)
	}
	got, err := mdRead(path)
	if err != nil {
		t.Fatalf("mdRead: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "a1" || got[0].Content != "hello" {
		t.Errorf("got %+v", got[0])
	}
	if got[0].Source != SourceUser {
		t.Errorf("source = %q", got[0].Source)
	}
}

func TestMD_UpsertReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "general.md")
	mdUpsert(path, Entry{ID: "a1", Content: "first", Source: SourceUser})
	mdUpsert(path, Entry{ID: "a1", Content: "second", Source: SourceUser})
	got, _ := mdRead(path)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (replaced, not appended)", len(got))
	}
	if got[0].Content != "second" {
		t.Errorf("content = %q", got[0].Content)
	}
}

func TestMD_UpsertAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "general.md")
	mdUpsert(path, Entry{ID: "a1", Content: "first", Source: SourceUser})
	mdUpsert(path, Entry{ID: "a2", Content: "second", Source: SourceAgent})
	got, _ := mdRead(path)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "a1" || got[1].ID != "a2" {
		t.Errorf("order: %+v", got)
	}
}

func TestMD_Delete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "general.md")
	mdUpsert(path, Entry{ID: "a1", Content: "first", Source: SourceUser})
	mdUpsert(path, Entry{ID: "a2", Content: "second", Source: SourceUser})
	if err := mdDelete(path, "a1"); err != nil {
		t.Fatalf("mdDelete: %v", err)
	}
	got, _ := mdRead(path)
	if len(got) != 1 || got[0].ID != "a2" {
		t.Errorf("after delete: %+v", got)
	}
}

func TestMD_DeleteNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "general.md")
	mdUpsert(path, Entry{ID: "a1", Content: "x", Source: SourceUser})
	if err := mdDelete(path, "missing"); err != nil {
		t.Fatalf("mdDelete(missing): %v", err)
	}
	got, _ := mdRead(path)
	if len(got) != 1 {
		t.Errorf("should not have removed anything: %+v", got)
	}
}

func TestMD_UpsertPreservesTimestamp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "general.md")
	when := time.Date(2026, 6, 6, 14, 23, 1, 0, time.UTC)
	mdUpsert(path, Entry{ID: "a1", Content: "x", Source: SourceUser, CreatedAt: when, UpdatedAt: when})
	got, _ := mdRead(path)
	if !got[0].CreatedAt.Equal(when) {
		t.Errorf("CreatedAt = %v, want %v", got[0].CreatedAt, when)
	}
}

func TestMD_RoundTripWithMultiline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "general.md")
	body := "line one\nline two\nline three"
	mdUpsert(path, Entry{ID: "a1", Content: body, Source: SourceUser})
	got, _ := mdRead(path)
	if got[0].Content != body {
		t.Errorf("content = %q, want %q", got[0].Content, body)
	}
}

func TestMD_ScopeFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{filepath.Join("/x", "general.md"), "general"},
		{filepath.Join("/x", "project-abc12345.md"), "project:abc12345"},
		{filepath.Join("/x", "scratch-2026-06-06.md"), "scratch:2026-06-06"},
		{filepath.Join("/x", "patterns", "auth-1.md"), "pattern:auth-1"},
		{filepath.Join("/x", "scope-foo.md"), ""},
	}
	for _, c := range cases {
		if got := scopeFromPath(c.path); got != c.want {
			t.Errorf("scopeFromPath(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestMD_WriteHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "general.md")
	mdUpsert(path, Entry{ID: "a1", Content: "x", Source: SourceUser})
	data, err := readFile(t, path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasPrefix(data, "# general memory\n") {
		t.Errorf("header = %q", firstNLines(data, 2))
	}
}

func firstNLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\\n")
}
