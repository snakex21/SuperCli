package ctxexec

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FailureSummary is the structured error the model sees when a
// sandboxed command fails: a deterministic "command_failed"
// first line (exit, duration, timeout) plus the capped
// stderr/stdout tails.

func TestFailureSummary_ExitWithStderr(t *testing.T) {
	r := &Result{
		ExitCode:   1,
		DurationMS: 1300,
		Stderr:     "FAIL: TestFoo\nexit status 1\n",
	}
	got := r.FailureSummary()
	if !strings.HasPrefix(got, "command_failed exit=1 (1.3s)") {
		t.Errorf("first line = %q", got)
	}
	if !strings.Contains(got, "stderr:\nFAIL: TestFoo") {
		t.Errorf("missing stderr tail:\n%s", got)
	}
}

func TestFailureSummary_IncludesStdoutTail(t *testing.T) {
	r := &Result{ExitCode: 2, DurationMS: 40, Stdout: "boom on line 7"}
	got := r.FailureSummary()
	if !strings.Contains(got, "stdout:\nboom on line 7") {
		t.Errorf("missing stdout tail:\n%s", got)
	}
}

func TestFailureSummary_Timeout(t *testing.T) {
	r := &Result{ExitCode: ExitTimeout, DurationMS: 10000, Stderr: "partial"}
	got := r.FailureSummary()
	if !strings.HasPrefix(got, "command_failed timeout exit=124 (10.0s)") {
		t.Errorf("first line = %q", got)
	}
	if !strings.Contains(got, "partial") {
		t.Errorf("missing partial stderr:\n%s", got)
	}
}

func TestFailureSummary_RunnerError(t *testing.T) {
	r := &Result{ExitCode: ExitNotFound, Error: "interpreter not found: python3"}
	got := r.FailureSummary()
	if !strings.HasPrefix(got, "command_failed exit=127: interpreter not found: python3") {
		t.Errorf("got %q", got)
	}
}

func TestFailureSummary_TailCapped(t *testing.T) {
	long := strings.Repeat("x", FailTailBytes+500) + "THE_END"
	r := &Result{ExitCode: 1, DurationMS: 5, Stderr: long}
	got := r.FailureSummary()
	if !strings.Contains(got, "stderr (tail, truncated):") {
		t.Errorf("missing truncation marker:\n%s", got[:200])
	}
	if !strings.Contains(got, "THE_END") {
		t.Error("tail must keep the LAST bytes")
	}
	if len(got) > FailTailBytes+200 {
		t.Errorf("summary too long: %d bytes", len(got))
	}
}

func TestFailureSummary_PreTruncatedFlagMarks(t *testing.T) {
	// Stream already truncated by the runner cap: the marker
	// must appear even when the remaining text is short.
	r := &Result{ExitCode: 1, DurationMS: 5, Stderr: "tail bit", TruncatedStderr: true}
	got := r.FailureSummary()
	if !strings.Contains(got, "stderr (tail, truncated):") {
		t.Errorf("missing marker for pre-truncated stream:\n%s", got)
	}
}

func TestFailureSummary_TailCutIsUTF8Safe(t *testing.T) {
	// Build a stream whose FailTailBytes-from-the-end cut lands
	// in the middle of a multi-byte rune: "ż" is 2 bytes, so a
	// run of them with one leading ASCII byte guarantees a
	// misaligned byte cut somewhere.
	long := "x" + strings.Repeat("ż", FailTailBytes)
	r := &Result{ExitCode: 1, DurationMS: 5, Stderr: long}
	got := r.FailureSummary()
	if !utf8.ValidString(got) {
		t.Fatalf("summary is not valid UTF-8:\n%q", got[:120])
	}
	// The tail must start at a rune boundary: locate the quoted
	// tail and check its first rune decodes cleanly.
	i := strings.Index(got, "stderr (tail, truncated):\n")
	if i < 0 {
		t.Fatalf("missing truncation marker:\n%s", got[:120])
	}
	tail := got[i+len("stderr (tail, truncated):\n"):]
	if r, _ := utf8.DecodeRuneInString(tail); r == utf8.RuneError {
		t.Errorf("tail starts mid-rune: %q", tail[:8])
	}
}

func TestFailureSummary_NoStreams(t *testing.T) {
	r := &Result{ExitCode: 3, DurationMS: 300}
	if got := r.FailureSummary(); got != "command_failed exit=3 (0.3s)" {
		t.Errorf("got %q", got)
	}
}
