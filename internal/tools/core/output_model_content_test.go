package core

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// ModelContent is the single contract point between a tool's
// Result{Text, Err} and the tool-result message the model sees.
// Historical bug: the loop used only Err, so diagnostics a tool
// returned in Text next to its error silently vanished
// (ctx_execute stderr, verifier "tool returned" bodies).

func TestModelContent_SuccessIsTextVerbatim(t *testing.T) {
	r := Result{Text: "plain output\nwith lines"}
	if got := r.ModelContent(); got != "plain output\nwith lines" {
		t.Errorf("happy path changed: %q", got)
	}
}

func TestModelContent_ErrOnly(t *testing.T) {
	r := Result{Err: errors.New("boom")}
	if got := r.ModelContent(); got != "error: boom" {
		t.Errorf("got %q", got)
	}
}

func TestModelContent_ErrPlusTextCarriesBoth(t *testing.T) {
	r := Result{Text: "stderr says: file busy", Err: errors.New("exit 1")}
	got := r.ModelContent()
	if !strings.HasPrefix(got, "error: exit 1") {
		t.Errorf("missing error head: %q", got)
	}
	if !strings.Contains(got, "tool output:\nstderr says: file busy") {
		t.Errorf("diagnostic Text dropped: %q", got)
	}
}

func TestModelContent_WhitespaceTextTreatedAsEmpty(t *testing.T) {
	r := Result{Text: "  \n\t ", Err: errors.New("boom")}
	if got := r.ModelContent(); got != "error: boom" {
		t.Errorf("got %q", got)
	}
}

func TestModelContent_DedupWhenErrContainsText(t *testing.T) {
	// Structured failures (command_failed exit=N + tail) already
	// embed the output; the same bytes must not be paid twice.
	r := Result{
		Text: "FAIL: TestFoo",
		Err:  errors.New("command_failed exit=1\noutput:\nFAIL: TestFoo"),
	}
	got := r.ModelContent()
	if n := strings.Count(got, "FAIL: TestFoo"); n != 1 {
		t.Errorf("text duplicated %d times:\n%s", n, got)
	}
}

func TestModelContent_SelfContainedErrSkipsText(t *testing.T) {
	// A tool can keep a rich Text for UIs (e.g. ctx_execute's
	// JSON) and mark the error self-contained so the model does
	// not receive the same streams twice.
	r := Result{
		Text: `{"stdout":"","stderr":"boom detail","exit_code":1}`,
		Err:  SelfContainedErr(errors.New("command_failed exit=1\nstderr:\nboom detail")),
	}
	got := r.ModelContent()
	if got != "error: command_failed exit=1\nstderr:\nboom detail" {
		t.Errorf("self-contained error must not append Text:\n%q", got)
	}
}

func TestModelContent_SelfContainedErrNilIsNil(t *testing.T) {
	if SelfContainedErr(nil) != nil {
		t.Error("SelfContainedErr(nil) must be nil")
	}
}

func TestModelContent_SelfContainedUnwraps(t *testing.T) {
	sentinel := errors.New("sentinel")
	if !errors.Is(SelfContainedErr(sentinel), sentinel) {
		t.Error("errors.Is must see through the wrapper")
	}
}

func TestModelContent_TextTailCappedAndUTF8Safe(t *testing.T) {
	// Long diagnostic Text: only the LAST ModelContentTailBytes
	// survive, cut on a rune boundary, with a truncation marker.
	long := "x" + strings.Repeat("ż", ModelContentTailBytes) + "THE_END"
	r := Result{Text: long, Err: errors.New("exit 1")}
	got := r.ModelContent()
	if !strings.Contains(got, "tool output (tail, truncated):") {
		t.Errorf("missing truncation marker:\n%s", got[:120])
	}
	if !strings.Contains(got, "THE_END") {
		t.Error("tail must keep the LAST bytes")
	}
	if len(got) > ModelContentTailBytes+200 {
		t.Errorf("content too long: %d bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Error("content is not valid UTF-8")
	}
	i := strings.Index(got, "tool output (tail, truncated):\n")
	tail := got[i+len("tool output (tail, truncated):\n"):]
	if r, _ := utf8.DecodeRuneInString(tail); r == utf8.RuneError {
		t.Errorf("tail starts mid-rune: %q", tail[:8])
	}
}

func TestModelContent_VerificationFailureShapeReachesModel(t *testing.T) {
	// The verifier's rewriteResultForFailure returns
	// Result{Text: "[verification failed] ... --- tool returned ---
	// <orig>", Err: reason}. Both the reason and the original tool
	// output must reach the model.
	res := rewriteResultForFailure(Result{Text: "claimed: wrote 3 files"}, "file missing on disk")
	got := res.ModelContent()
	if !strings.HasPrefix(got, "error: file missing on disk") {
		t.Errorf("missing reason head: %q", got)
	}
	if !strings.Contains(got, "claimed: wrote 3 files") {
		t.Errorf("original tool output dropped: %q", got)
	}
}
