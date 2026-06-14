package fileops

import (
	"os"
	"strings"
	"testing"
)

// readBack returns the on-disk lines (1-based content) for
// assertions. Fails the test on read error.
func readBack(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readBack: %v", err)
	}
	s := string(data)
	if strings.HasSuffix(s, "\n") {
		s = s[:len(s)-1]
	}
	return strings.Split(s, "\n")
}

// --- EditLineAnchored: happy path ---

func TestEditLineAnchored_ExactHint(t *testing.T) {
	path := tmpFile(t, "alpha\nbeta\ngamma\n")
	diff, err := EditLineAnchored(path, 2, "beta", "BETA")
	if err != nil {
		t.Fatalf("EditLineAnchored: %v", err)
	}
	got := readBack(t, path)
	if got[1] != "BETA" {
		t.Errorf("line 2 = %q, want BETA", got[1])
	}
	if got[0] != "alpha" || got[2] != "gamma" {
		t.Errorf("neighbours mutated: %q", got)
	}
	if !strings.Contains(diff, "BETA") || !strings.Contains(diff, "beta") {
		t.Errorf("diff missing old/new: %q", diff)
	}
}

// --- EditLineAnchored: drift (hint off, content found nearby) ---

func TestEditLineAnchored_DriftDown(t *testing.T) {
	// Target content sits at line 5 but the model thinks it's
	// at line 2 (e.g. imports were added above since it read).
	path := tmpFile(t, "a\nb\nc\nd\ntarget\ne\n")
	_, err := EditLineAnchored(path, 2, "target", "TARGET")
	if err != nil {
		t.Fatalf("EditLineAnchored drift: %v", err)
	}
	got := readBack(t, path)
	if got[4] != "TARGET" {
		t.Errorf("line 5 = %q, want TARGET (found by content)", got[4])
	}
}

func TestEditLineAnchored_DriftUp(t *testing.T) {
	// Content is above the hinted line.
	path := tmpFile(t, "x\ntarget\ny\nz\nw\nq\n")
	_, err := EditLineAnchored(path, 5, "target", "TARGET")
	if err != nil {
		t.Fatalf("EditLineAnchored drift up: %v", err)
	}
	got := readBack(t, path)
	if got[1] != "TARGET" {
		t.Errorf("line 2 = %q, want TARGET", got[1])
	}
}

// --- EditLineAnchored: hint wins over duplicate content ---

func TestEditLineAnchored_HintDisambiguatesDuplicates(t *testing.T) {
	// "return nil" repeats; the exact hint must win and touch
	// only the pointed-at line.
	path := tmpFile(t, "return nil\nx\nreturn nil\ny\nreturn nil\n")
	_, err := EditLineAnchored(path, 3, "return nil", "return err")
	if err != nil {
		t.Fatalf("EditLineAnchored: %v", err)
	}
	got := readBack(t, path)
	if got[2] != "return err" {
		t.Errorf("line 3 = %q, want return err", got[2])
	}
	if got[0] != "return nil" || got[4] != "return nil" {
		t.Errorf("wrong duplicate touched: %q", got)
	}
}

// --- EditLineAnchored: error paths (fail loud, no write) ---

func TestEditLineAnchored_ContentNotFound(t *testing.T) {
	path := tmpFile(t, "a\nb\nc\n")
	_, err := EditLineAnchored(path, 2, "nonexistent", "X")
	if err == nil {
		t.Fatal("want error for missing content, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want 'not found'", err)
	}
	if got := readBack(t, path); got[1] != "b" {
		t.Errorf("file mutated on failure: %q", got)
	}
}

func TestEditLineAnchored_AmbiguousNearby(t *testing.T) {
	// Two matches inside the window, none on the hinted line.
	path := tmpFile(t, "dup\nfiller\ndup\n")
	_, err := EditLineAnchored(path, 2, "dup", "X")
	if err == nil {
		t.Fatal("want ambiguity error, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error = %v, want 'ambiguous'", err)
	}
	if got := readBack(t, path); got[0] != "dup" || got[2] != "dup" {
		t.Errorf("file mutated on ambiguity: %q", got)
	}
}

func TestEditLineAnchored_EmptyExpected(t *testing.T) {
	path := tmpFile(t, "a\nb\n")
	_, err := EditLineAnchored(path, 1, "", "X")
	if err == nil {
		t.Fatal("want error for empty anchor, got nil")
	}
	if !strings.Contains(err.Error(), "non-empty") {
		t.Errorf("error = %v, want 'non-empty'", err)
	}
}

func TestEditLineAnchored_BadLine(t *testing.T) {
	path := tmpFile(t, "a\n")
	if _, err := EditLineAnchored(path, 0, "a", "X"); err == nil {
		t.Fatal("want error for line=0, got nil")
	}
}

func TestEditLineAnchored_EmptyFile(t *testing.T) {
	path := tmpFile(t, "")
	_, err := EditLineAnchored(path, 1, "anything", "X")
	if err == nil {
		t.Fatal("want error for empty file, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %v, want 'empty'", err)
	}
}

// --- EditLineAnchored: edge — hint beyond EOF, content in window ---

func TestEditLineAnchored_HintBeyondEOF(t *testing.T) {
	// Hint points past end of file, but content is within
	// AnchorSearchRadius of the clamped window.
	path := tmpFile(t, "a\nb\ntarget\n")
	_, err := EditLineAnchored(path, 8, "target", "TARGET")
	if err != nil {
		t.Fatalf("EditLineAnchored beyond EOF: %v", err)
	}
	if got := readBack(t, path); got[2] != "TARGET" {
		t.Errorf("line 3 = %q, want TARGET", got[2])
	}
}

// --- EditLineAnchored: edge — drift exactly at radius boundary ---

func TestEditLineAnchored_AtRadiusBoundary(t *testing.T) {
	// Content sits exactly AnchorSearchRadius lines below hint.
	lines := make([]string, 0, AnchorSearchRadius+2)
	lines = append(lines, "hint-here")
	for i := 0; i < AnchorSearchRadius-1; i++ {
		lines = append(lines, "filler")
	}
	lines = append(lines, "edge-target")
	path := tmpFile(t, strings.Join(lines, "\n")+"\n")
	// hint=1, target at index AnchorSearchRadius (line radius+1)
	_, err := EditLineAnchored(path, 1, "edge-target", "EDGE")
	if err != nil {
		t.Fatalf("at boundary: %v", err)
	}
	got := readBack(t, path)
	if got[AnchorSearchRadius] != "EDGE" {
		t.Errorf("boundary line = %q, want EDGE", got[AnchorSearchRadius])
	}
}
