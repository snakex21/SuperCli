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

// --- EditLine ---

func TestEditLine_Basic(t *testing.T) {
	path := tmpFile(t, "line1\nline2\nline3\n")
	diff, err := EditLine(path, 2, "NEW_LINE")
	if err != nil {
		t.Fatalf("EditLine: %v", err)
	}
	if !strings.Contains(diff, "NEW_LINE") {
		t.Errorf("diff missing new content: %s", diff)
	}
	// Verify file content.
	got, _ := ReadLines(path, 2, 2)
	if got[0].Content != "NEW_LINE" {
		t.Errorf("file line 2 = %q, want NEW_LINE", got[0].Content)
	}
}

func TestEditLine_ShowsContext(t *testing.T) {
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = strings.Repeat("x", 10)
	}
	path := tmpFile(t, strings.Join(lines, "\n")+"\n")
	diff, err := EditLine(path, 5, "CHANGED")
	if err != nil {
		t.Fatalf("EditLine: %v", err)
	}
	// Should show context lines (with " " prefix) and
	// the change (with "-" and "+" prefixes).
	if !strings.Contains(diff, "-") || !strings.Contains(diff, "+") {
		t.Errorf("diff should contain - and + prefixes: %s", diff)
	}
}

func TestEditLine_LineExceedsLength(t *testing.T) {
	path := tmpFile(t, "a\n")
	_, err := EditLine(path, 5, "new")
	if err == nil {
		t.Fatal("expected error for line > file length")
	}
}

func TestEditLine_LineBelow1(t *testing.T) {
	path := tmpFile(t, "a\n")
	_, err := EditLine(path, 0, "new")
	if err == nil {
		t.Fatal("expected error for line < 1")
	}
}

func TestEditLine_FirstLine(t *testing.T) {
	path := tmpFile(t, "first\nsecond\n")
	_, err := EditLine(path, 1, "NEW_FIRST")
	if err != nil {
		t.Fatalf("EditLine: %v", err)
	}
	got, _ := ReadLines(path, 1, 1)
	if got[0].Content != "NEW_FIRST" {
		t.Errorf("line 1 = %q, want NEW_FIRST", got[0].Content)
	}
}

func TestEditLine_LastLine(t *testing.T) {
	path := tmpFile(t, "a\nb\n")
	_, err := EditLine(path, 2, "NEW_B")
	if err != nil {
		t.Fatalf("EditLine: %v", err)
	}
	got, _ := ReadLines(path, 2, 2)
	if got[0].Content != "NEW_B" {
		t.Errorf("line 2 = %q, want NEW_B", got[0].Content)
	}
}

// --- InsertAfter ---

func TestInsertAfter_Basic(t *testing.T) {
	path := tmpFile(t, "line1\nline2\n")
	diff, err := InsertAfter(path, 1, "INSERTED")
	if err != nil {
		t.Fatalf("InsertAfter: %v", err)
	}
	if !strings.Contains(diff, "INSERTED") {
		t.Errorf("diff missing inserted content: %s", diff)
	}
	got, _ := ReadLines(path, 1, 3)
	if len(got) != 3 {
		t.Fatalf("want 3 lines, got %d", len(got))
	}
	if got[1].Content != "INSERTED" {
		t.Errorf("line 2 = %q, want INSERTED", got[1].Content)
	}
}

func TestInsertAfter_AtEnd(t *testing.T) {
	path := tmpFile(t, "line1\n")
	_, err := InsertAfter(path, 1, "APPENDED")
	if err != nil {
		t.Fatalf("InsertAfter: %v", err)
	}
	got, _ := ReadLines(path, 2, 2)
	if got[0].Content != "APPENDED" {
		t.Errorf("line 2 = %q, want APPENDED", got[0].Content)
	}
}

func TestInsertAfter_LineExceedsLength(t *testing.T) {
	path := tmpFile(t, "a\n")
	_, err := InsertAfter(path, 5, "x")
	if err == nil {
		t.Fatal("expected error for line > file length")
	}
}

func TestInsertAfter_LineBelow1(t *testing.T) {
	path := tmpFile(t, "a\n")
	_, err := InsertAfter(path, 0, "x")
	if err == nil {
		t.Fatal("expected error for line < 1")
	}
}

// --- DeleteLines ---

func TestDeleteLines_Basic(t *testing.T) {
	path := tmpFile(t, "a\nb\nc\nd\n")
	diff, err := DeleteLines(path, 2, 3)
	if err != nil {
		t.Fatalf("DeleteLines: %v", err)
	}
	if !strings.Contains(diff, "-") {
		t.Errorf("diff should show deletions: %s", diff)
	}
	got, _ := ReadLines(path, 1, 2)
	if len(got) != 2 {
		t.Fatalf("want 2 lines, got %d", len(got))
	}
	if got[0].Content != "a" || got[1].Content != "d" {
		t.Errorf("remaining: %q, %q, want a, d", got[0].Content, got[1].Content)
	}
}

func TestDeleteLines_SingleLine(t *testing.T) {
	path := tmpFile(t, "a\nb\nc\n")
	_, err := DeleteLines(path, 2, 2)
	if err != nil {
		t.Fatalf("DeleteLines: %v", err)
	}
	got, _ := ReadLines(path, 1, 2)
	if len(got) != 2 {
		t.Fatalf("want 2 lines, got %d", len(got))
	}
	if got[0].Content != "a" || got[1].Content != "c" {
		t.Errorf("remaining: %q, %q, want a, c", got[0].Content, got[1].Content)
	}
}

func TestDeleteLines_ToExceedsLength(t *testing.T) {
	path := tmpFile(t, "a\nb\n")
	_, err := DeleteLines(path, 1, 10)
	if err != nil {
		t.Fatalf("DeleteLines: to > length should be capped: %v", err)
	}
	got, _ := ReadLines(path, 1, 1)
	if len(got) != 0 {
		t.Errorf("want 0 lines, got %d", len(got))
	}
}

func TestDeleteLines_FromExceedsLength(t *testing.T) {
	path := tmpFile(t, "a\n")
	_, err := DeleteLines(path, 5, 10)
	if err == nil {
		t.Fatal("expected error for from > file length")
	}
}

func TestDeleteLines_ToBelowFrom(t *testing.T) {
	path := tmpFile(t, "a\nb\n")
	_, err := DeleteLines(path, 3, 1)
	if err == nil {
		t.Fatal("expected error for to < from")
	}
}

func TestDeleteLines_ShowsContext(t *testing.T) {
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = "line"
	}
	path := tmpFile(t, strings.Join(lines, "\n")+"\n")
	diff, err := DeleteLines(path, 5, 5)
	if err != nil {
		t.Fatalf("DeleteLines: %v", err)
	}
	// Should show context " " lines and deleted "-" line.
	if !strings.Contains(diff, "-") {
		t.Errorf("diff should contain deletion marker: %s", diff)
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

func TestEditLine_PreservesOtherLines(t *testing.T) {
	path := tmpFile(t, "a\nb\nc\nd\ne\n")
	_, err := EditLine(path, 3, "CHANGED")
	if err != nil {
		t.Fatalf("EditLine: %v", err)
	}
	got, _ := ReadLines(path, 1, 5)
	want := []string{"a", "b", "CHANGED", "d", "e"}
	for i, w := range want {
		if got[i].Content != w {
			t.Errorf("line %d = %q, want %q", i+1, got[i].Content, w)
		}
	}
}

func TestInsertAfter_PreservesOtherLines(t *testing.T) {
	path := tmpFile(t, "a\nb\nc\n")
	_, err := InsertAfter(path, 2, "INSERTED")
	if err != nil {
		t.Fatalf("InsertAfter: %v", err)
	}
	got, _ := ReadLines(path, 1, 4)
	want := []string{"a", "b", "INSERTED", "c"}
	for i, w := range want {
		if got[i].Content != w {
			t.Errorf("line %d = %q, want %q", i+1, got[i].Content, w)
		}
	}
}
