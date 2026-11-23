package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/tools"
)

// Worker 312: a worker continued (send_message) during an automatic wake-up
// turn kept working after that turn ended. Its file changes were refused
// ("checkpoint turn no longer accepts mutations") 214 times. Now the wake
// turn has its own checkpoint turn, the worker borrows it, and its
// create_file, make_dir and patch_file run and can be undone.
func TestWorkerContinuedInWakeTurnWritesAndIsRewindable(t *testing.T) {
	parallelNativeTest(t)
	home := t.TempDir()
	writeCheckpointFixture(t, filepath.Join(home, "src", "bed.zig"), "const color = 0;\n")
	m := openSnapshotTestManager(t, home)
	wake := m.NewTurn("minecraft-session", "") // an automatic turn: no user message
	borrow, err := BorrowInvocation(WithTurn(context.Background(), wake, &wake.barrier), true)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, fn func() error) tools.Tool {
		return wake.Wrap(tools.Tool{UncheckedArgs: true, Name: name, Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: name + " ok"}, fn()
		}})
	}
	calls := []struct {
		tool tools.Tool
		args string
	}{
		{write("create_file", func() error {
			return os.WriteFile(filepath.Join(home, "src", "bed_tiles.zig"), []byte("pub const tiles = 1;\n"), 0o644)
		}), `{"path":"src/bed_tiles.zig"}`},
		{write("make_dir", func() error { return os.MkdirAll(filepath.Join(home, "round2-baseline"), 0o755) }), `{"path":"round2-baseline"}`},
		{write("patch_file", func() error {
			return os.WriteFile(filepath.Join(home, "src", "bed.zig"), []byte("const color = 14;\n"), 0o644)
		}), `{"path":"src/bed.zig"}`},
	}
	// The wake turn ends (its UI run completes) while the worker goes on.
	completed := make(chan *Record, 1)
	if _, err := wake.CompleteDeferred(context.Background(), func(record *Record, err error) {
		if err != nil {
			t.Error(err)
		}
		completed <- record
	}); err != nil {
		t.Fatal(err)
	}
	ctx := WithoutTurnFallback(borrow.Bind(context.Background()))
	for _, c := range calls {
		result, err := c.tool.Fn(ctx, json.RawMessage(c.args))
		if err := errors.Join(err, result.Err); err != nil {
			t.Fatalf("%s refused after the wake turn ended: %v", c.tool.Name, err)
		}
		if strings.Contains(result.Text, "No restore point") {
			t.Fatalf("%s ran without a restore point although the worker holds its turn: %q", c.tool.Name, result.Text)
		}
	}
	borrow.Close() // the worker's run ends
	var record *Record
	select {
	case record = <-completed:
	case <-time.After(20 * time.Second):
		t.Fatal("the wake turn's checkpoint was never completed")
	}
	if record == nil {
		t.Fatal("no checkpoint record for the worker's changes")
	}
	if _, err := m.Undo(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "src", "bed.zig"), []byte("const color = 0;\n"))
	if _, err := os.Stat(filepath.Join(home, "src", "bed_tiles.zig")); !os.IsNotExist(err) {
		t.Fatalf("undo kept the created file: %v", err)
	}
}

// When no restore point can be taken at all (a detached run with no turn,
// or a turn that is already recorded), the tool runs anyway and says so.
func TestFileToolRunsWithoutRestorePointInsteadOfRefusing(t *testing.T) {
	parallelNativeTest(t)
	home := t.TempDir()
	m := openSnapshotTestManager(t, home)
	ran := 0
	spec := tools.Tool{UncheckedArgs: true, Name: "create_file", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		ran++
		return tools.Result{Text: "created"}, os.WriteFile(filepath.Join(home, "x.txt"), []byte("x"), 0o644)
	}}
	// 1. A finished turn (recorded, no borrower).
	done := m.NewTurn("s", "p")
	if _, err := done.Complete(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := done.Wrap(spec).Fn(context.Background(), json.RawMessage(`{"path":"x.txt"}`))
	if err := errors.Join(err, result.Err); err != nil || ran != 1 || !strings.Contains(result.Text, NoRestorePointNotice) {
		t.Fatalf("closed turn: ran=%d err=%v text=%q", ran, err, result.Text)
	}
	// 2. A detached worker run without its own binding never borrows the
	// controller's newest turn, and still runs.
	controller := NewController(m, "s")
	controller.Start("another user turn")
	active := controller.currentTurn()
	result, err = controller.Wrap(spec).Fn(WithoutTurnFallback(context.Background()), json.RawMessage(`{"path":"x.txt"}`))
	if err := errors.Join(err, result.Err); err != nil || ran != 2 || !strings.Contains(result.Text, NoRestorePointNotice) {
		t.Fatalf("detached run: ran=%d err=%v text=%q", ran, err, result.Text)
	}
	if active.touched {
		t.Fatal("a detached worker's change was attributed to an unrelated user turn")
	}
}
