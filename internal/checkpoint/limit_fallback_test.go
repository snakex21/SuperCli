package checkpoint

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/tools"
)

func runLimitedCommand(t *testing.T, turn *Turn, args string, fn func()) tools.Result {
	t.Helper()
	spec := turn.Wrap(tools.Tool{Name: "ctx_execute", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		fn()
		return tools.Result{Text: "done", ModelPreview: "done"}, nil
	}})
	result, err := spec.Fn(context.Background(), json.RawMessage(args))
	if err != nil || result.Err != nil {
		t.Fatalf("command failed: err=%v result=%v", err, result.Err)
	}
	return result
}

func assertFileAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s should not exist: %v", path, err)
	}
}

// (a) Whole workspace over the limit, workdir inside it: the baseline is
// narrowed to workdir and undo never touches files outside that scope.
func TestOverLimitCommandNarrowsCheckpointToWorkdir(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	home := t.TempDir()
	writeOversizedSnapshotFixture(t, home)
	inside := filepath.Join(home, "sub", "a.txt")
	created := filepath.Join(home, "sub", "new.txt")
	deleted := filepath.Join(home, "sub", "gone.txt")
	outside := filepath.Join(home, "outside.txt")
	writeCheckpointFixture(t, inside, "before")
	writeCheckpointFixture(t, deleted, "restore me")
	writeCheckpointFixture(t, outside, "outside-before")
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("narrow", "command in workdir")
	result := runLimitedCommand(t, turn, `{"command":["x"],"workdir":"sub"}`, func() {
		writeCheckpointFixture(t, inside, "after")
		writeCheckpointFixture(t, created, "created")
		writeCheckpointFixture(t, outside, "outside-after")
		if err := os.Remove(deleted); err != nil {
			t.Fatal(err)
		}
	})
	for _, text := range []string{result.Text, result.ModelPreview} {
		if !strings.Contains(text, `ograniczony do workdir "sub"`) || !strings.Contains(text, "limit") {
			t.Fatalf("missing narrowing notice: %q", text)
		}
	}
	record, err := turn.Complete(ctx)
	want := []string{"sub/a.txt", "sub/gone.txt", "sub/new.txt"}
	if err != nil || record == nil || !reflect.DeepEqual(record.Files, want) {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, inside, []byte("before"))
	assertFileBytes(t, deleted, []byte("restore me"))
	assertFileAbsent(t, created)
	// Outside the snapshot scope: neither restored nor deleted.
	assertFileBytes(t, outside, []byte("outside-after"))
	if info, err := os.Stat(filepath.Join(home, "unrelated-large-archive.bin")); err != nil || info.Size() != maxSnapshotFileBytes+1 {
		t.Fatalf("large out-of-scope file changed: %v", err)
	}
	if _, err := m.Redo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, inside, []byte("after"))
	assertFileBytes(t, created, []byte("created"))
	assertFileBytes(t, outside, []byte("outside-after"))
}

// (b) Workdir itself over the limit: the command runs with a visible warning,
// no record is created and nothing is restored.
func TestOverLimitWorkdirRunsCommandWithWarning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	home := t.TempDir()
	big := filepath.Join(home, "big")
	if err := os.MkdirAll(big, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(big, "huge.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxSnapshotFileBytes + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	small := filepath.Join(big, "small.txt")
	writeCheckpointFixture(t, small, "before")
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("warn", "command in huge workdir")
	called := false
	result := runLimitedCommand(t, turn, `{"command":["x"],"workdir":"big"}`, func() {
		called = true
		writeCheckpointFixture(t, small, "after")
	})
	if !called || turn.touched {
		t.Fatalf("called=%v touched=%v", called, turn.touched)
	}
	if !strings.Contains(result.Text, "bez checkpointu: przekroczony limit") || !strings.Contains(result.ModelPreview, "bez checkpointu") {
		t.Fatalf("missing warning: text=%q preview=%q", result.Text, result.ModelPreview)
	}
	record, err := turn.Complete(ctx)
	if err != nil || record != nil {
		t.Fatalf("uncheckpointed turn produced record=%+v err=%v", record, err)
	}
	assertFileBytes(t, small, []byte("after"))
}

// A workdir outside the workspace or equal to it is never used as a scope.
func TestCommandWorkdirRootRejectsWorkspaceAndOutside(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeCheckpointFixture(t, filepath.Join(home, "sub", "a.txt"), "x")
	m := openSnapshotTestManager(t, home)
	outside := filepath.ToSlash(t.TempDir())
	for args, want := range map[string]string{
		`{"workdir":"sub"}`:             "sub",
		`{"workdir":"sub/"}`:            "sub",
		`{"workdir":"."}`:               "",
		`{}`:                            "",
		`{"workdir":"../escape"}`:       "",
		`{"workdir":"` + outside + `"}`: "",
		`{"workdir":"missing-dir"}`:     "missing-dir",
	} {
		got, ok := m.commandWorkdirRoot(json.RawMessage(args))
		if got != want || ok != (want != "") {
			t.Errorf("%s: got %q ok=%v, want %q", args, got, ok, want)
		}
	}
}

// An earlier scoped baseline in the same turn survives a later uncheckpointed
// over-limit command: undo restores only that earlier scope.
func TestOverLimitCommandKeepsEarlierScopedBaseline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	home := t.TempDir()
	writeOversizedSnapshotFixture(t, home)
	a := filepath.Join(home, "a.txt")
	b := filepath.Join(home, "b.txt")
	writeCheckpointFixture(t, a, "original-a")
	writeCheckpointFixture(t, b, "original-b")
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("mixed", "edit then command")
	runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(home).Spec()), `{"path":"a.txt","content":"edited"}`)
	result := runLimitedCommand(t, turn, `{"command":["x"]}`, func() {
		writeCheckpointFixture(t, a, "command-a")
		writeCheckpointFixture(t, b, "command-b")
	})
	if !strings.Contains(result.Text, "bez checkpointu") {
		t.Fatalf("missing warning: %q", result.Text)
	}
	record, err := turn.Complete(ctx)
	if err != nil || record == nil || !reflect.DeepEqual(record.Files, []string{"a.txt"}) {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, a, []byte("original-a"))
	assertFileBytes(t, b, []byte("command-b"))
}
