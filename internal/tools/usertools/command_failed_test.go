package usertools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
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
