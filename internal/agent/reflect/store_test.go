package reflect

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"supercli/internal/storage/memory"
)

func newTestStore(t *testing.T) (*Store, *memory.Store, string) {
	t.Helper()
	dir := t.TempDir()
	mem, err := memory.OpenStore(dir)
	if err != nil {
		t.Fatalf("memory.OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = mem.Close() })
	rs, err := NewStore(mem)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return rs, mem, dir
}

func samplePattern(id, kind, title string) Pattern {
	return Pattern{
		ID:          id,
		Title:       title,
		Kind:        Kind(kind),
		Description: "test pattern body",
		Tool:        "echo",
		Category:    "model",
		Reason:      "schema mismatch",
		Count:       3,
		Confidence:  0.6,
		Tags:        []string{"echo", "model"},
		CreatedAt:   time.Now().UTC(),
	}
}

func TestStore_SaveAndLoad(t *testing.T) {
	rs, _, _ := newTestStore(t)
	p := samplePattern(HashPattern(KindError, "demo"), "error", "demo: schema mismatch")
	if err := rs.Save(context.Background(), &p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := rs.Load(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !ok {
		t.Fatal("Load returned not-found")
	}
	if got.ID != p.ID {
		t.Errorf("ID = %q, want %q", got.ID, p.ID)
	}
	if got.Title != p.Title {
		t.Errorf("Title = %q, want %q", got.Title, p.Title)
	}
	if got.Kind != p.Kind {
		t.Errorf("Kind = %q, want %q", got.Kind, p.Kind)
	}
	if got.Description == "" {
		t.Error("Description empty after round-trip")
	}
}

func TestStore_SaveOverwrites(t *testing.T) {
	rs, _, _ := newTestStore(t)
	p1 := samplePattern("abc12345", "error", "first: alpha")
	p1.Description = "first body"
	_ = rs.Save(context.Background(), &p1)

	p2 := samplePattern("abc12345", "error", "first: alpha")
	p2.Description = "second body"
	_ = rs.Save(context.Background(), &p2)

	got, _, _ := rs.Load(context.Background(), "abc12345")
	if got.Description != "second body" {
		t.Errorf("Description = %q, want %q (overwrite)", got.Description, "second body")
	}
}

func TestStore_SaveFillsIDAndTime(t *testing.T) {
	rs, _, _ := newTestStore(t)
	p := Pattern{
		Kind:        KindError,
		Title:       "no id",
		Description: "x",
	}
	if p.ID != "" {
		t.Fatal("test setup wrong: ID should be empty")
	}
	if err := rs.Save(context.Background(), &p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if p.ID == "" {
		t.Error("Save did not fill ID")
	}
	if p.CreatedAt.IsZero() {
		t.Error("Save did not fill CreatedAt")
	}
}

func TestStore_LoadMissing(t *testing.T) {
	rs, _, _ := newTestStore(t)
	_, ok, err := rs.Load(context.Background(), "nonexistent")
	if err != nil {
		t.Errorf("err = %v, want nil for not-found", err)
	}
	if ok {
		t.Error("ok = true, want false")
	}
}

func TestStore_List(t *testing.T) {
	rs, _, _ := newTestStore(t)
	patterns := []Pattern{
		samplePattern("aaaaaaaa", "error", "low conf"),
		samplePattern("bbbbbbbb", "error", "high conf"),
		samplePattern("cccccccc", "error", "mid conf"),
	}
	patterns[0].Confidence = 0.2
	patterns[1].Confidence = 0.9
	patterns[2].Confidence = 0.5
	for i := range patterns {
		if err := rs.Save(context.Background(), &patterns[i]); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	got, err := rs.List(context.Background(), 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List returned %d, want 3", len(got))
	}
	// First is highest confidence.
	if got[0].Confidence != 0.9 {
		t.Errorf("first confidence = %f, want 0.9", got[0].Confidence)
	}
}

func TestStore_ListLimit(t *testing.T) {
	rs, _, _ := newTestStore(t)
	hex := []string{"aaaaaaaa", "bbbbbbbb", "cccccccc", "dddddddd", "eeeeeeee"}
	for i, id := range hex {
		p := samplePattern(id, "error", "x")
		p.Confidence = float64(i) / 10.0
		_ = rs.Save(context.Background(), &p)
	}
	got, _ := rs.List(context.Background(), 3)
	if len(got) != 3 {
		t.Errorf("List(3) returned %d, want 3", len(got))
	}
}

func TestStore_Delete(t *testing.T) {
	rs, _, _ := newTestStore(t)
	p := samplePattern("dddddddd", "error", "to delete")
	_ = rs.Save(context.Background(), &p)

	deleted, err := rs.Delete(context.Background(), "dddddddd")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !deleted {
		t.Error("Delete returned false, want true")
	}
	_, ok, _ := rs.Load(context.Background(), "dddddddd")
	if ok {
		t.Error("Load after Delete returned ok=true")
	}

	// Second delete is a no-op.
	deleted2, _ := rs.Delete(context.Background(), "dddddddd")
	if deleted2 {
		t.Error("second Delete returned true, want false")
	}
}

func TestStore_NewStore_NilMem(t *testing.T) {
	_, err := NewStore(nil)
	if err == nil {
		t.Error("NewStore(nil) returned nil err")
	}
}

func TestStore_ContextCancel(t *testing.T) {
	rs, _, _ := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := samplePattern("eeeeeeee", "error", "x")
	err := rs.Save(ctx, &p)
	if err == nil {
		t.Error("Save on cancelled ctx returned nil err")
	}
}

func TestStore_MarkdownSyncWritesFile(t *testing.T) {
	rs, _, dir := newTestStore(t)
	p := samplePattern("ffffffff", "error", "file: written")
	_ = rs.Save(context.Background(), &p)
	// memory.Store writes a markdown file per scope. The
	// pattern scope maps to <root>/patterns/<id>.md via
	// ScopeFile.
	patternFile := filepath.Join(dir, "memory", "patterns", "ffffffff.md")
	if _, err := os.Stat(patternFile); err != nil {
		t.Errorf("markdown not written at %s: %v", patternFile, err)
	}
}

func TestHashPattern_Stable(t *testing.T) {
	a := HashPattern(KindError, "search_code: rg missing")
	b := HashPattern(KindError, "search_code: rg missing")
	if a != b {
		t.Errorf("HashPattern unstable: %s vs %s", a, b)
	}
	c := HashPattern(KindError, "SEARCH_CODE: rg missing")
	// Case-insensitive on title.
	if a != c {
		t.Errorf("HashPattern should be case-insensitive on title: %s vs %s", a, c)
	}
	d := HashPattern(KindCombo, "search_code: rg missing")
	if a == d {
		t.Error("HashPattern ignored Kind")
	}
}

func TestHashPattern_Length(t *testing.T) {
	id := HashPattern(KindError, "x")
	if len(id) != 8 {
		t.Errorf("HashPattern length = %d, want 8", len(id))
	}
}
