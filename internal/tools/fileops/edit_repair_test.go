package fileops

import (
	"strings"
	"testing"
)

// 4 of the 7 observed edit_line failures handed a multi-line expected_old to a
// tool that replaces exactly one line, and the generic "not found" answer read
// like line drift — the model resent the identical call. These tests assert the
// failure now names the real cause and carries the text needed to fix the call.

func TestEditLineAnchored_RejectsMultilineExpectedOld(t *testing.T) {
	path := writeTemp(t, "main.go", "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n")

	multi := "func main() {\n\tprintln(\"hi\")\n}"
	_, err := EditLineAnchored(path, 3, multi, "func main() { println(\"bye\") }")
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "expected_old spans 3 lines") {
		t.Fatalf("message does not report the line span: %s", msg)
	}
	if !strings.Contains(msg, "use patch_file") {
		t.Fatalf("message does not redirect to patch_file: %s", msg)
	}
	if !strings.Contains(msg, "nothing changed") {
		t.Fatalf("message lost the no-write guarantee: %s", msg)
	}
}

// The observed calls ran to ~60 lines; the span must be reported, not clipped.
func TestEditLineAnchored_ReportsLargeLineSpan(t *testing.T) {
	path := writeTemp(t, "big.go", strings.Repeat("x\n", 100))
	block := strings.Repeat("y\n", 59) + "y"
	_, err := EditLineAnchored(path, 10, block, "z")
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(err.Error(), "expected_old spans 60 lines") {
		t.Fatalf("wrong span: %s", err.Error())
	}
}

// The multi-line verdict must win over every other diagnosis, including the
// out-of-range hint — otherwise the model is told to fix the line number when
// the payload shape is what is wrong.
func TestEditLineAnchored_MultilineVerdictBeatsRangeCheck(t *testing.T) {
	path := writeTemp(t, "short.txt", "one\ntwo\n")
	_, err := EditLineAnchored(path, 900, "a\nb", "x")
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(err.Error(), "expected_old spans 2 lines") {
		t.Fatalf("range check shadowed the real cause: %s", err.Error())
	}
}

// A single-line anchor that misses must hand back the text that IS there.
func TestEditLineAnchored_ShowsActualNearbyLines(t *testing.T) {
	path := writeTemp(t, "cfg.yaml", "alpha: 1\nbeta: 2\ngamma: 3\ndelta: 4\n")

	_, err := EditLineAnchored(path, 3, "gamma: 99", "gamma: 5")
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	for _, want := range []string{"beta: 2", "gamma: 3", "delta: 4"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message does not show %q: %s", want, msg)
		}
	}
}

func TestEditLineAnchored_ReportsHintPastEOF(t *testing.T) {
	path := writeTemp(t, "short.txt", "one\ntwo\n")
	_, err := EditLineAnchored(path, 400, "whatever", "x")
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(err.Error(), "past the end of the file (2 lines)") {
		t.Fatalf("message does not report the file length: %s", err.Error())
	}
}

// Regression: the success path is untouched — same diff, byte for byte, and no
// diagnostic text leaking into it.
func TestEditLineAnchored_SuccessPathUnchanged(t *testing.T) {
	path := writeTemp(t, "ok.txt", "alpha\nbeta\ngamma\n")
	diff, err := EditLineAnchored(path, 2, "beta", "BETA")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	want := "    1: alpha\n-   2: beta\n+   2: BETA\n    3: gamma\n"
	if diff != want {
		t.Fatalf("diff changed:\ngot  %q\nwant %q", diff, want)
	}
	for _, forbidden := range []string{"whitespace", "single line", "spans", "patch_file"} {
		if strings.Contains(diff, forbidden) {
			t.Fatalf("diagnostic text leaked into the success path: %q", diff)
		}
	}
}
