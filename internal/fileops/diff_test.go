package fileops

import (
	"os"
	"strings"
	"testing"
)

func TestTracker_Record(t *testing.T) {
	tr := NewTracker(10)
	tr.Record("/tmp/test.txt", "write", "", "hello world")
	if tr.Count() != 1 {
		t.Fatalf("Count() = %d, want 1", tr.Count())
	}
	chs := tr.Changes()
	if chs[0].Path != "/tmp/test.txt" {
		t.Errorf("Path = %q, want /tmp/test.txt", chs[0].Path)
	}
	if chs[0].Op != "write" {
		t.Errorf("Op = %q, want write", chs[0].Op)
	}
}

func TestTracker_RingBuffer(t *testing.T) {
	tr := NewTracker(3)
	for i := 0; i < 5; i++ {
		tr.Record("/f"+string(rune('0'+i)), "write", "", "content")
	}
	if tr.Count() != 3 {
		t.Fatalf("Count() = %d, want 3", tr.Count())
	}
	// Oldest should be dropped; we should have /f2, /f3, /f4.
	chs := tr.Changes()
	if chs[0].Path != "/f2" {
		t.Errorf("first change Path = %q, want /f2", chs[0].Path)
	}
}

func TestTracker_Clear(t *testing.T) {
	tr := NewTracker(10)
	tr.Record("/a", "write", "", "x")
	tr.Record("/b", "edit", "old", "new")
	tr.Clear()
	if tr.Count() != 0 {
		t.Fatalf("Count() = %d after Clear, want 0", tr.Count())
	}
}

func TestDiffOutput_Empty(t *testing.T) {
	tr := NewTracker(10)
	if out := tr.DiffOutput(); out != "" {
		t.Errorf("DiffOutput() = %q, want empty", out)
	}
}

func TestDiffOutput_ShowsChanges(t *testing.T) {
	tr := NewTracker(10)
	tr.Record("/tmp/a.txt", "write", "", "line1\nline2")
	tr.Record("/tmp/b.txt", "edit", "old content", "new content")
	out := tr.DiffOutput()
	if !strings.Contains(out, "2 file change(s)") {
		t.Errorf("missing count header: %q", out)
	}
	if !strings.Contains(out, "/tmp/a.txt") {
		t.Errorf("missing path /tmp/a.txt: %q", out)
	}
	if !strings.Contains(out, "+ line1") {
		t.Errorf("missing added line: %q", out)
	}
	if !strings.Contains(out, "- old content") {
		t.Errorf("missing removed line: %q", out)
	}
}

func TestDiffOutput_TruncatesLongContent(t *testing.T) {
	tr := NewTracker(10)
	// Build 50 lines.
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "line"
	}
	tr.Record("/big.txt", "write", "", strings.Join(lines, "\n"))
	out := tr.DiffOutput()
	if !strings.Contains(out, "more lines") {
		t.Errorf("expected truncation indicator: %q", out)
	}
}

func TestTracker_OpTypes(t *testing.T) {
	tr := NewTracker(10)
	tr.Record("/a", "write", "", "new")
	tr.Record("/b", "edit", "old", "new")
	tr.Record("/c", "insert", "", "inserted")
	tr.Record("/d", "deleted", "deleted", "")
	chs := tr.Changes()
	ops := []string{"write", "edit", "insert", "deleted"}
	for i, op := range ops {
		if chs[i].Op != op {
			t.Errorf("change %d Op = %q, want %q", i, chs[i].Op, op)
		}
	}
}

func TestUndo_SingleChange(t *testing.T) {
	// Create a temp file with initial content.
	dir := t.TempDir()
	path := dir + "/test.txt"
	os.WriteFile(path, []byte("original"), 0o644)

	tr := NewTracker(10)
	// Record simulates what the tool does: it records old and new content.
	// The tool itself writes the new content to the file.
	os.WriteFile(path, []byte("modified"), 0o644)
	tr.Record(path, "edit", "original", "modified")

	// Verify the file has the new content.
	data, _ := os.ReadFile(path)
	if string(data) != "modified" {
		t.Fatalf("pre-undo content = %q, want modified", string(data))
	}

	undone, err := tr.Undo(1)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if len(undone) != 1 {
		t.Fatalf("undone = %d, want 1", len(undone))
	}
	if undone[0].Path != path {
		t.Fatalf("undone path = %q", undone[0].Path)
	}

	// File should have original content.
	data, _ = os.ReadFile(path)
	if string(data) != "original" {
		t.Fatalf("post-undo content = %q, want original", string(data))
	}
	if tr.Count() != 0 {
		t.Fatalf("count after undo = %d, want 0", tr.Count())
	}
}

func TestUndo_MultipleChanges(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/test.txt"
	os.WriteFile(path, []byte("v1"), 0o644)

	tr := NewTracker(10)
	tr.Record(path, "edit", "v1", "v2")
	tr.Record(path, "edit", "v2", "v3")

	undone, err := tr.Undo(2)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if len(undone) != 2 {
		t.Fatalf("undone = %d, want 2", len(undone))
	}

	data, _ := os.ReadFile(path)
	if string(data) != "v1" {
		t.Fatalf("post-undo content = %q, want v1", string(data))
	}
}

func TestUndo_NoChanges(t *testing.T) {
	tr := NewTracker(10)
	_, err := tr.Undo(1)
	if err == nil {
		t.Fatal("Undo on empty tracker should fail")
	}
}

func TestUndoResult(t *testing.T) {
	r := UndoResult{Path: "/a", Op: "edit"}
	if r.Path != "/a" {
		t.Fatalf("Path = %q", r.Path)
	}
}
