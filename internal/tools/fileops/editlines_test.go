package fileops

import (
	"strings"
	"testing"
)

// --- EditLinesAnchored: happy path, multiple edits ---

func TestEditLinesAnchored_MultipleApply(t *testing.T) {
	path := tmpFile(t, "alpha\nbeta\ngamma\ndelta\n")
	_, err := EditLinesAnchored(path, []AnchoredEdit{
		{Line: 1, ExpectedOld: "alpha", NewContent: "ALPHA"},
		{Line: 3, ExpectedOld: "gamma", NewContent: "GAMMA"},
	})
	if err != nil {
		t.Fatalf("EditLinesAnchored: %v", err)
	}
	got := readBack(t, path)
	want := []string{"ALPHA", "beta", "GAMMA", "delta"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("line %d = %q, want %q (all: %q)", i+1, got[i], w, got)
		}
	}
}

// --- EditLinesAnchored: bottom-to-top correctness ---
// Two edits where the SAME literal content appears on different
// lines. If edits were applied top-to-bottom with naive
// re-resolution after each write, the second anchor could match
// the already-rewritten first line. Validating up-front against
// the original file + bottom-to-top application keeps each edit
// on its intended line.

func TestEditLinesAnchored_BottomToTop(t *testing.T) {
	// "x" appears on lines 1 and 3.
	path := tmpFile(t, "x\nmiddle\nx\nlast\n")
	_, err := EditLinesAnchored(path, []AnchoredEdit{
		{Line: 1, ExpectedOld: "x", NewContent: "FIRST"},
		{Line: 3, ExpectedOld: "x", NewContent: "THIRD"},
	})
	if err != nil {
		t.Fatalf("EditLinesAnchored: %v", err)
	}
	got := readBack(t, path)
	want := []string{"FIRST", "middle", "THIRD", "last"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("line %d = %q, want %q (all: %q)", i+1, got[i], w, got)
		}
	}
}

// --- EditLinesAnchored: any mismatch fails the whole call, no write ---

func TestEditLinesAnchored_MismatchFailsAtomic(t *testing.T) {
	orig := "alpha\nbeta\ngamma\n"
	path := tmpFile(t, orig)
	_, err := EditLinesAnchored(path, []AnchoredEdit{
		{Line: 1, ExpectedOld: "alpha", NewContent: "ALPHA"}, // valid
		{Line: 2, ExpectedOld: "WRONG", NewContent: "X"},     // bad anchor
	})
	if err == nil {
		t.Fatal("want error for bad anchor, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want 'not found'", err)
	}
	// All-or-nothing: even the valid first edit must NOT be applied.
	got := readBack(t, path)
	if got[0] != "alpha" || got[1] != "beta" || got[2] != "gamma" {
		t.Errorf("file mutated despite failure: %q", got)
	}
}

// --- EditLinesAnchored: stale line numbers self-correct by content ---

func TestEditLinesAnchored_StaleLineNumbers(t *testing.T) {
	// Model's line hints are all off by +5 (e.g. it read an
	// older version with extra imports). Content still anchors.
	path := tmpFile(t, "one\ntwo\nthree\nfour\n")
	_, err := EditLinesAnchored(path, []AnchoredEdit{
		{Line: 6, ExpectedOld: "one", NewContent: "ONE"},
		{Line: 9, ExpectedOld: "four", NewContent: "FOUR"},
	})
	if err != nil {
		t.Fatalf("EditLinesAnchored with stale lines: %v", err)
	}
	got := readBack(t, path)
	want := []string{"ONE", "two", "three", "FOUR"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("line %d = %q, want %q (all: %q)", i+1, got[i], w, got)
		}
	}
}

// --- EditLinesAnchored: two edits resolving to the same line fail ---

func TestEditLinesAnchored_ConflictingTargets(t *testing.T) {
	path := tmpFile(t, "a\nsame\nc\n")
	_, err := EditLinesAnchored(path, []AnchoredEdit{
		{Line: 2, ExpectedOld: "same", NewContent: "X"},
		{Line: 2, ExpectedOld: "same", NewContent: "Y"},
	})
	if err == nil {
		t.Fatal("want error for conflicting targets, got nil")
	}
	if !strings.Contains(err.Error(), "target") {
		t.Errorf("error = %v, want mention of target conflict", err)
	}
	if got := readBack(t, path); got[1] != "same" {
		t.Errorf("file mutated on conflict: %q", got)
	}
}

// --- EditLinesAnchored: empty edit list errors ---

func TestEditLinesAnchored_NoEdits(t *testing.T) {
	path := tmpFile(t, "a\n")
	if _, err := EditLinesAnchored(path, nil); err == nil {
		t.Fatal("want error for empty edit list, got nil")
	}
}

// --- EditLinesAnchored: missing anchor proof errors, no write ---

func TestEditLinesAnchored_EmptyAnchor(t *testing.T) {
	path := tmpFile(t, "a\nb\n")
	_, err := EditLinesAnchored(path, []AnchoredEdit{
		{Line: 1, ExpectedOld: "", NewContent: "X"},
	})
	if err == nil {
		t.Fatal("want error for empty expected_old, got nil")
	}
	if got := readBack(t, path); got[0] != "a" {
		t.Errorf("file mutated: %q", got)
	}
}
