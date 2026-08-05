package fileops

import (
	"os"
	"strings"
	"testing"
)

func writeFileT(t *testing.T, content string) string {
	t.Helper()
	path := tmpFile(t, "")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(data)
}

// --- the one-line change: what edit_line used to be for ---

// The bare one-line change: `old` is the code without the file's leading tab.
// It already worked as a plain substring match, and it must keep working —
// this is the ergonomics edit_line existed for.
func TestPatchFile_OneLine_BareCodeNoContext(t *testing.T) {
	path := writeFileT(t, "func add() int {\n\treturn 2 - 1\n}\n")
	if _, err := PatchFile(path, []PatchChange{{Old: "return 2 - 1", New: "return 2 + 1"}}, ""); err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	if got := readAll(t, path); got != "func add() int {\n\treturn 2 + 1\n}\n" {
		t.Errorf("content = %q", got)
	}
}

// The model over-indents the line it copied. The shared extra indent is
// removed again and the file's own indentation is kept.
func TestPatchFile_OneLine_ExtraIndentRemoved(t *testing.T) {
	path := writeFileT(t, "alpha\nbeta\n")
	res, err := PatchFile(path, []PatchChange{{Old: "\t\tbeta", New: "\t\tBETA"}}, "")
	if err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	if got := readAll(t, path); got != "alpha\nBETA\n" {
		t.Errorf("content = %q", got)
	}
	if !strings.Contains(res.Note, "whitespace normalised") {
		t.Errorf("success note should disclose the relaxed match, got %q", res.Note)
	}
}

// An exact byte match is still applied byte-for-byte, even when the file's
// line is indented and the replacement's extra lines are not. Relaxation
// deliberately does not reach here: the bytes the model sent were found, and
// re-indenting them would be the tool guessing at intent on the success path.
// The boundary is asserted so it stays a decision rather than an accident.
func TestPatchFile_ExactMatchIsNotReindented(t *testing.T) {
	path := writeFileT(t, "if x {\n        doA(1)\n}\n")
	if _, err := PatchFile(path, []PatchChange{{Old: "doA(1)\n", New: "doA(1)\ndoB(2)\n"}}, ""); err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	if got := readAll(t, path); got != "if x {\n        doA(1)\ndoB(2)\n}\n" {
		t.Errorf("content = %q", got)
	}
}

// The same insertion written against the file's own indentation is relaxed
// into place: the model under-indented the whole block, so the file's indent
// is applied to every line of the replacement and relative structure survives.
func TestPatchFile_UnderIndentedBlockInheritsIndent(t *testing.T) {
	path := writeFileT(t, "if x {\n        doA(1)\n        doZ()\n}\n")
	old := "doA(1)\n        doZ()\n"
	if _, err := PatchFile(path, []PatchChange{{Old: old, New: "doA(1)\n        doB(2)\n        doZ()\n"}}, ""); err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	want := "if x {\n        doA(1)\n        doB(2)\n        doZ()\n}\n"
	if got := readAll(t, path); got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

// Trailing whitespace on the file's line is not something the model can see
// in a rendered read, so it must not be what breaks the patch.
func TestPatchFile_TrailingWhitespaceInFileIgnored(t *testing.T) {
	path := writeFileT(t, "\talpha   \nbeta\n")
	if _, err := PatchFile(path, []PatchChange{{Old: "\talpha\n", New: "\tALPHA\n"}}, ""); err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	if got := readAll(t, path); got != "\tALPHA\nbeta\n" {
		t.Errorf("content = %q", got)
	}
}

// --- CRLF: the Windows miss ---

func TestPatchFile_LFOldAgainstCRLFFile(t *testing.T) {
	path := writeFileT(t, "one\r\ntwo\r\nthree\r\n")
	res, err := PatchFile(path, []PatchChange{{Old: "one\ntwo", New: "ONE\nTWO"}}, "")
	if err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	if got := readAll(t, path); got != "ONE\r\nTWO\r\nthree\r\n" {
		t.Errorf("content = %q (CRLF must be preserved)", got)
	}
	if !strings.Contains(res.Note, "line endings") {
		t.Errorf("note = %q", res.Note)
	}
}

func TestPatchFile_CRLFOldAgainstLFFile(t *testing.T) {
	path := writeFileT(t, "one\ntwo\nthree\n")
	if _, err := PatchFile(path, []PatchChange{{Old: "one\r\ntwo", New: "ONE\r\nTWO"}}, ""); err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	if got := readAll(t, path); got != "ONE\nTWO\nthree\n" {
		t.Errorf("content = %q (LF must be preserved)", got)
	}
}

func TestPatchFile_MultiLineBlockIndentDropped(t *testing.T) {
	path := writeFileT(t, "func f() {\n\tif a {\n\t\tb()\n\t}\n}\n")
	old := "if a {\n\tb()\n}"
	if _, err := PatchFile(path, []PatchChange{{Old: old, New: "if a {\n\tc()\n}"}}, ""); err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	want := "func f() {\n\tif a {\n\t\tc()\n\t}\n}\n"
	if got := readAll(t, path); got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

// --- the relaxation must not make patching sloppier ---

// An exact match that occurs twice stays an ambiguity error with its line
// list. Relaxing here would let the tool pick a block the model did not mean.
func TestPatchFile_AmbiguousExactStillFails(t *testing.T) {
	path := writeFileT(t, "\tx()\nsomething\n\tx()\n")
	_, err := PatchFile(path, []PatchChange{{Old: "\tx()", New: "\ty()"}}, "")
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !strings.Contains(err.Error(), "occurs 2 times") || !strings.Contains(err.Error(), "at lines 1,3") {
		t.Errorf("error must say how many and where, got: %v", err)
	}
}

// The same ambiguity reached only through relaxation must fail too, not pick
// the first hit.
func TestPatchFile_AmbiguousAfterRelaxationFails(t *testing.T) {
	const before = "\tx()\nsomething\nx()\n"
	path := writeFileT(t, before)
	_, err := PatchFile(path, []PatchChange{{Old: "\t\tx()", New: "\t\ty()"}}, "")
	if err == nil {
		t.Fatal("expected failure: two lines match once indentation is ignored")
	}
	if got := readAll(t, path); got != before {
		t.Errorf("file must be untouched, got %q", got)
	}
}

// expected_count still governs a relaxed match, and each hit keeps its own
// indentation.
func TestPatchFile_RelaxedHonoursExpectedCount(t *testing.T) {
	path := writeFileT(t, "\tx()\nsomething\nx()\n")
	if _, err := PatchFile(path, []PatchChange{{Old: "\t\tx()", New: "\t\ty()", ExpectedCount: 2}}, ""); err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	if got := readAll(t, path); got != "\ty()\nsomething\ny()\n" {
		t.Errorf("content = %q", got)
	}
}

// A mid-line fragment is never fuzzily spliced: only whole lines relax.
func TestPatchFile_MidLineFragmentDoesNotRelax(t *testing.T) {
	path := writeFileT(t, "x := foo( bar )\n")
	_, err := PatchFile(path, []PatchChange{{Old: "foo(bar)", New: "foo(baz)"}}, "")
	if err == nil {
		t.Fatal("a mid-line fragment must not match after whitespace relaxation")
	}
	if got := readAll(t, path); got != "x := foo( bar )\n" {
		t.Errorf("file must be untouched, got %q", got)
	}
}

// Tabs against spaces is a real difference the tool must not guess at.
func TestPatchFile_MixedIndentStyleRefused(t *testing.T) {
	path := writeFileT(t, "if a {\n\t    b()\n}\n")
	_, err := PatchFile(path, []PatchChange{{Old: "        b()", New: "        c()"}}, "")
	if err == nil {
		t.Fatal("space indent against tab+space indent must not silently match")
	}
}

// Relaxation is a fallback, never a substitute: an exact hit wins and its
// bytes are used verbatim.
func TestPatchFile_ExactStillPreferred(t *testing.T) {
	path := writeFileT(t, "\tvalue\nvalue\n")
	if _, err := PatchFile(path, []PatchChange{{Old: "value\n", New: "VALUE\n"}}, ""); err == nil {
		t.Fatal("old occurs twice exactly; must report the ambiguity")
	}
	if _, err := PatchFile(path, []PatchChange{{Old: "\tvalue\n", New: "\tVALUE\n"}}, ""); err != nil {
		t.Fatalf("exact match: %v", err)
	}
	if got := readAll(t, path); got != "\tVALUE\nvalue\n" {
		t.Errorf("content = %q", got)
	}
}

// Relaxed changes stay atomic with the exact ones around them.
func TestPatchFile_RelaxedIsAtomicWithRest(t *testing.T) {
	before := "alpha\n\tbeta\n"
	path := writeFileT(t, before)
	_, err := PatchFile(path, []PatchChange{
		{Old: "beta", New: "BETA"},
		{Old: "nowhere in this file at all", New: "x"},
	}, "")
	if err == nil {
		t.Fatal("expected failure on the second change")
	}
	if got := readAll(t, path); got != before {
		t.Errorf("first (relaxed) change must not have been written: %q", got)
	}
}

func TestPatchFile_NoNewlineAtEOF(t *testing.T) {
	path := writeFileT(t, "alpha\n\tbeta")
	if _, err := PatchFile(path, []PatchChange{{Old: "beta", New: "BETA"}}, ""); err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	if got := readAll(t, path); got != "alpha\n\tBETA" {
		t.Errorf("content = %q", got)
	}
}

func TestPatchFile_OldWithTrailingNewlineRelaxed(t *testing.T) {
	path := writeFileT(t, "a\n\tb\nc\n")
	if _, err := PatchFile(path, []PatchChange{{Old: "b\n", New: "B\n"}}, ""); err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
	if got := readAll(t, path); got != "a\n\tB\nc\n" {
		t.Errorf("content = %q", got)
	}
}
