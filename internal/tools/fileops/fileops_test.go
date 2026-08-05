package fileops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper: write a temp file with given content, return path.
func tmpFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("tmpFile: %v", err)
	}
	return path
}

// --- ReadLines ---

func TestReadLines_Basic(t *testing.T) {
	path := tmpFile(t, "line1\nline2\nline3\n")
	got, err := ReadLines(path, 1, 2)
	if err != nil {
		t.Fatalf("ReadLines: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 lines, got %d", len(got))
	}
	if got[0].Number != 1 || got[0].Content != "line1" {
		t.Errorf("line 1: %+v", got[0])
	}
	if got[1].Number != 2 || got[1].Content != "line2" {
		t.Errorf("line 2: %+v", got[1])
	}
}

func TestReadLines_SingleLine(t *testing.T) {
	path := tmpFile(t, "only\n")
	got, err := ReadLines(path, 1, 1)
	if err != nil {
		t.Fatalf("ReadLines: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 line, got %d", len(got))
	}
	if got[0].Content != "only" {
		t.Errorf("content = %q, want only", got[0].Content)
	}
}

func TestReadLines_CapsAtFileEnd(t *testing.T) {
	path := tmpFile(t, "a\nb\n")
	got, err := ReadLines(path, 1, 100)
	if err != nil {
		t.Fatalf("ReadLines: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want 2 lines (capped), got %d", len(got))
	}
}

func TestReadLines_ExceedsCap(t *testing.T) {
	path := tmpFile(t, "a\nb\n")
	_, err := ReadLines(path, 1, 600)
	if err == nil {
		t.Fatal("expected error for range > MaxLineRange")
	}
}

func TestReadLines_FromBelow1(t *testing.T) {
	path := tmpFile(t, "a\n")
	_, err := ReadLines(path, 0, 5)
	if err == nil {
		t.Fatal("expected error for from < 1")
	}
}

func TestReadLines_ToBelowFrom(t *testing.T) {
	path := tmpFile(t, "a\nb\n")
	_, err := ReadLines(path, 3, 1)
	if err == nil {
		t.Fatal("expected error for to < from")
	}
}

func TestReadLines_FromExceedsLength(t *testing.T) {
	path := tmpFile(t, "a\nb\n")
	_, err := ReadLines(path, 5, 10)
	if err == nil {
		t.Fatal("expected error for from > file length")
	}
}

func TestReadLines_EmptyFile(t *testing.T) {
	path := tmpFile(t, "")
	got, err := ReadLines(path, 1, 1)
	if err == nil {
		t.Fatal("expected error for empty file")
	}
	_ = got
}

func TestReadLines_NoTrailingNewline(t *testing.T) {
	path := tmpFile(t, "line1\nline2")
	got, err := ReadLines(path, 1, 2)
	if err != nil {
		t.Fatalf("ReadLines: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 lines, got %d", len(got))
	}
	if got[1].Content != "line2" {
		t.Errorf("content = %q, want line2", got[1].Content)
	}
}

// --- ReadContext ---

func TestReadContext_Basic(t *testing.T) {
	path := tmpFile(t, "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n")
	got, err := ReadContext(path, 5, 2)
	if err != nil {
		t.Fatalf("ReadContext: %v", err)
	}
	// lines 3..7 (5±2)
	if len(got) != 5 {
		t.Fatalf("want 5 lines, got %d", len(got))
	}
	if got[0].Number != 3 || got[4].Number != 7 {
		t.Errorf("range: %d..%d, want 3..7", got[0].Number, got[4].Number)
	}
}

func TestReadContext_DefaultRadius(t *testing.T) {
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = "line"
	}
	path := tmpFile(t, strings.Join(lines, "\n")+"\n")
	got, err := ReadContext(path, 15, 0)
	if err != nil {
		t.Fatalf("ReadContext: %v", err)
	}
	// 15 ± 10 = lines 5..25 = 21 lines
	if len(got) != 21 {
		t.Errorf("want 21 lines, got %d", len(got))
	}
}

func TestReadContext_RadiusClamped(t *testing.T) {
	// A huge radius must be clamped to MaxContextRadius so a
	// single call can't dump an entire large file.
	lines := make([]string, 2000)
	for i := range lines {
		lines[i] = "line"
	}
	path := tmpFile(t, strings.Join(lines, "\n")+"\n")
	got, err := ReadContext(path, 1000, 100000)
	if err != nil {
		t.Fatalf("ReadContext: %v", err)
	}
	maxLines := 2*MaxContextRadius + 1
	if len(got) > maxLines {
		t.Fatalf("radius not clamped: got %d lines, want <= %d", len(got), maxLines)
	}
}

func TestReadContext_AtStart(t *testing.T) {
	path := tmpFile(t, "a\nb\nc\n")
	got, err := ReadContext(path, 1, 5)
	if err != nil {
		t.Fatalf("ReadContext: %v", err)
	}
	if got[0].Number != 1 {
		t.Errorf("first line = %d, want 1", got[0].Number)
	}
}

func TestReadContext_AtEnd(t *testing.T) {
	path := tmpFile(t, "a\nb\nc\n")
	got, err := ReadContext(path, 3, 5)
	if err != nil {
		t.Fatalf("ReadContext: %v", err)
	}
	if got[len(got)-1].Number != 3 {
		t.Errorf("last line = %d, want 3", got[len(got)-1].Number)
	}
}

func TestReadContext_LineExceedsLength(t *testing.T) {
	path := tmpFile(t, "a\n")
	_, err := ReadContext(path, 10, 2)
	if err == nil {
		t.Fatal("expected error for line > file length")
	}
}

func TestReadContext_LineBelow1(t *testing.T) {
	path := tmpFile(t, "a\n")
	_, err := ReadContext(path, 0, 2)
	if err == nil {
		t.Fatal("expected error for line < 1")
	}
}

// --- Edge cases ---

func TestReadLines_LineWithSpecialChars(t *testing.T) {
	path := tmpFile(t, "line with spaces\nline\"with\"quotes\nline\\with\\backslash\n")
	got, err := ReadLines(path, 1, 3)
	if err != nil {
		t.Fatalf("ReadLines: %v", err)
	}
	if got[1].Content != "line\"with\"quotes" {
		t.Errorf("quotes: %q", got[1].Content)
	}
	if got[2].Content != "line\\with\\backslash" {
		t.Errorf("backslash: %q", got[2].Content)
	}
}
