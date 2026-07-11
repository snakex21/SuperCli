package usertools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	core "supercli/internal/tools/core"
)

// commandFailedErr is the structured failure a user tool's
// process produces: "command_failed exit=N" (or timeout) plus
// the capped output tail.

func TestCommandFailedErr_ExitError(t *testing.T) {
	// Run the test binary itself with a bogus flag: a portable
	// way to obtain a real *exec.ExitError.
	out, err := exec.Command(os.Args[0], "-test.bogusflag").CombinedOutput()
	if err == nil {
		t.Skip("bogus flag unexpectedly succeeded")
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Skipf("not an ExitError: %v", err)
	}
	got := commandFailedErr(context.Background(), err, string(out))
	if !strings.HasPrefix(got.Error(), "command_failed exit=") {
		t.Errorf("got %q, want command_failed exit= prefix", got)
	}
	if !strings.Contains(got.Error(), "output") {
		t.Errorf("got %q, want output tail", got)
	}
}

func TestCommandFailedErr_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), -time.Second)
	defer cancel()
	got := commandFailedErr(ctx, errors.New("signal: killed"), "partial output")
	if !strings.HasPrefix(got.Error(), "command_failed timeout") {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(got.Error(), "partial output") {
		t.Errorf("got %q, want partial output", got)
	}
}

func TestCommandFailedErr_StartFailurePassesReason(t *testing.T) {
	got := commandFailedErr(context.Background(),
		errors.New(`exec: "nope": executable file not found in $PATH`), "")
	want := `command_failed: exec: "nope": executable file not found in $PATH`
	if got.Error() != want {
		t.Errorf("got %q", got)
	}
}

func TestCommandFailedErr_TailCapped(t *testing.T) {
	long := strings.Repeat("y", failTailBytes+300) + "LAST_LINE"
	got := commandFailedErr(context.Background(), errors.New("boom"), long)
	if !strings.Contains(got.Error(), "output (tail, truncated):") {
		t.Error("missing truncation marker")
	}
	if !strings.Contains(got.Error(), "LAST_LINE") {
		t.Error("tail must keep the LAST bytes")
	}
	if len(got.Error()) > failTailBytes+200 {
		t.Errorf("error too long: %d bytes", len(got.Error()))
	}
}

func TestCommandFailedErr_TailCutIsUTF8Safe(t *testing.T) {
	// A run of 2-byte runes with one leading ASCII byte makes the
	// failTailBytes-from-the-end cut land mid-rune unless the cut
	// is moved to a rune boundary.
	long := "x" + strings.Repeat("ż", failTailBytes)
	got := commandFailedErr(context.Background(), errors.New("boom"), long)
	if !utf8.ValidString(got.Error()) {
		t.Fatalf("error is not valid UTF-8: %q", got.Error()[:120])
	}
	i := strings.Index(got.Error(), "output (tail, truncated):\n")
	if i < 0 {
		t.Fatal("missing truncation marker")
	}
	tail := got.Error()[i+len("output (tail, truncated):\n"):]
	if r, _ := utf8.DecodeRuneInString(tail); r == utf8.RuneError {
		t.Errorf("tail starts mid-rune: %q", tail[:8])
	}
}

func TestCommandFailedErr_IsSelfContained(t *testing.T) {
	// The error already embeds the output tail, so ModelContent
	// must not append Result.Text a second time.
	got := commandFailedErr(context.Background(), errors.New("boom"), "some output")
	res := core.Result{Text: "some output", Err: got}
	mc := res.ModelContent()
	if n := strings.Count(mc, "some output"); n != 1 {
		t.Errorf("output appears %d times in ModelContent, want 1:\n%s", n, mc)
	}
}
