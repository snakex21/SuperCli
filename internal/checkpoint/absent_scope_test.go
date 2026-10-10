package checkpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"supercli/internal/tools"
)

// A missing Git executable makes any accidentally repeated capture fail. The
// usage receipt must also stay byte-identical: no new checkpoint writes occur.
// Completion and actual Undo/Redo still use real Git after restoring PATH.
func TestAbsentScopeExpansionReusesLiveBeforeAndRestoresCreatedFiles(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writeCheckpointFixture(t, filepath.Join(home, "original.txt"), "before\r\n")
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("absent-files", "edit and create files")
	write := turn.Wrap(tools.NewWriteFile(home).Spec())
	runSnapshotTool(t, write, `{"path":"original.txt","content":"after\r\n"}`)
	before := turn.before
	ledger := filepath.Join(filepath.Dir(m.gate.path), storeUsageName)
	usageBefore, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir())
	created := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("new-%d.bin", i)
		created = append(created, name)
		args, err := json.Marshal(map[string]string{"path": name, "content": "created\r\n"})
		if err != nil {
			t.Fatal(err)
		}
		runSnapshotTool(t, write, string(args))
	}
	usageAfter, err := os.ReadFile(ledger)
	if err != nil || !bytes.Equal(usageBefore, usageAfter) || turn.before != before {
		t.Fatalf("absent expansion rewrote checkpoint: before=%s now=%s usageChanged=%v err=%v", before, turn.before, !bytes.Equal(usageBefore, usageAfter), err)
	}
	for _, name := range created {
		if !scopeContains(turn.scopeRoots, name) {
			t.Fatalf("missing after-capture scope: %s", name)
		}
	}
	t.Setenv("PATH", originalPath)
	// An external retention pass cannot collect the still-pinned baseline.
	if _, err := m.git(ctx, "prune", "--expire=now"); err != nil {
		t.Fatal(err)
	}
	record, err := turn.Complete(ctx)
	if err != nil || record == nil || len(record.Files) != 9 {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "original.txt"), []byte("before\r\n"))
	for _, name := range created {
		if _, err := os.Lstat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Fatalf("created file survived undo: %s %v", name, err)
		}
	}
	if _, err := m.Redo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "original.txt"), []byte("after\r\n"))
	for _, name := range created {
		assertFileBytes(t, filepath.Join(home, name), []byte("created\r\n"))
	}
}

func TestAbsentScopeExpansionKeepsCaptureForExistingOrUnpinnedPaths(t *testing.T) {
	for _, kind := range []string{"first-missing", "file", "empty-directory", "unpinned", "whole"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			home := t.TempDir()
			m := openSnapshotTestManager(t, home)
			turn := m.NewTurn("fallback", kind)
			if kind != "first-missing" {
				if kind == "whole" {
					if err := turn.ensureBefore(ctx); err != nil {
						t.Fatal(err)
					}
				} else if err := turn.ensureBeforeForTool(ctx, "create_file", json.RawMessage(`{"path":"first.txt"}`)); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "file" {
				// An empty file exists and must never be treated as absent.
				writeCheckpointFixture(t, filepath.Join(home, "next.txt"), "")
			} else if kind == "empty-directory" {
				if err := os.Mkdir(filepath.Join(home, "next.txt"), 0700); err != nil {
					t.Fatal(err)
				}
			} else if kind == "unpinned" {
				turn.beforePinned = false
			}
			roots := append([]string(nil), turn.scopeRoots...)
			t.Setenv("PATH", t.TempDir())
			if err := turn.ensureBeforeForTool(ctx, "create_file", json.RawMessage(`{"path":"next.txt"}`)); err == nil {
				t.Fatal("required capture was skipped despite missing Git")
			}
			if !reflect.DeepEqual(roots, turn.scopeRoots) || turn.beforePinned {
				t.Fatal("failed capture extended scope or marked a valid pin")
			}
		})
	}
}

func TestAbsentScopeExpansionCancellationDoesNotExtendScope(t *testing.T) {
	home := t.TempDir()
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("canceled", "create")
	if err := turn.ensureBeforeForTool(context.Background(), "create_file", json.RawMessage(`{"path":"first.txt"}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	roots := append([]string(nil), turn.scopeRoots...)
	if err := turn.ensureBeforeForTool(ctx, "create_file", json.RawMessage(`{"path":"next.txt"}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if !reflect.DeepEqual(roots, turn.scopeRoots) {
		t.Fatal("canceled scope expansion changed roots")
	}
}

func TestBeforePinRetryKeepsOriginalBytesForCoveredAndWholeTurns(t *testing.T) {
	for _, whole := range []bool{false, true} {
		t.Run(fmt.Sprintf("whole-%t", whole), func(t *testing.T) {
			ctx := context.Background()
			home := t.TempDir()
			writeCheckpointFixture(t, filepath.Join(home, "original.txt"), "first-before\r\n")
			m := openSnapshotTestManager(t, home)
			turn := m.NewTurn("pin-retry", "edit")
			if whole {
				if err := turn.ensureBefore(ctx); err != nil {
					t.Fatal(err)
				}
			}
			write := turn.Wrap(tools.NewWriteFile(home).Spec())
			runSnapshotTool(t, write, `{"path":"original.txt","content":"changed"}`)
			before := turn.before
			// Simulate a partial publish: the ref has advanced, while Turn.before
			// must still describe the last fully successful original baseline.
			advanced, err := m.captureSnapshot(ctx, []string{"original.txt"}, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.git(ctx, "update-ref", turn.active.root()+"/before", advanced); err != nil {
				t.Fatal(err)
			}
			turn.beforePinned = false
			originalPath := os.Getenv("PATH")
			t.Setenv("PATH", t.TempDir())
			called := false
			spec := turn.Wrap(tools.Tool{Name: "write_file", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				called = true
				return tools.Result{}, nil
			}})
			res, err := spec.Fn(ctx, json.RawMessage(`{"path":"original.txt"}`))
			if err != nil || res.Err == nil || called || turn.beforePinned {
				t.Fatalf("unrepaired pin permitted mutation: called=%v res=%v err=%v", called, res.Err, err)
			}
			t.Setenv("PATH", originalPath)
			if whole {
				if err := turn.ensureBefore(ctx); err != nil {
					t.Fatal(err)
				}
			}
			runSnapshotTool(t, write, `{"path":"original.txt","content":"final"}`)
			pin, err := m.git(ctx, "rev-parse", turn.active.root()+"/before")
			if err != nil || !turn.beforePinned || turn.before != before || pin != before+"\n" {
				t.Fatalf("original pin not restored: before=%s now=%s pin=%q err=%v", before, turn.before, pin, err)
			}
			record, err := turn.Complete(ctx)
			if err != nil || record == nil {
				t.Fatalf("record=%+v err=%v", record, err)
			}
			if _, err := m.Undo(ctx, record.ID); err != nil {
				t.Fatal(err)
			}
			assertFileBytes(t, filepath.Join(home, "original.txt"), []byte("first-before\r\n"))
		})
	}
}

func TestBeforePinRetryMissingObjectBlocksMutation(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("missing-before", "edit")
	if err := turn.ensureBeforeForTool(ctx, "create_file", json.RawMessage(`{"path":"first.txt"}`)); err != nil {
		t.Fatal(err)
	}
	original := turn.before
	turn.before, turn.beforePinned = "0123456789abcdef0123456789abcdef01234567", false
	called := false
	spec := turn.Wrap(tools.Tool{Name: "create_file", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		called = true
		return tools.Result{}, nil
	}})
	res, err := spec.Fn(ctx, json.RawMessage(`{"path":"first.txt"}`))
	if err != nil || res.Err == nil || called || turn.beforePinned {
		t.Fatalf("missing before object permitted mutation: called=%v res=%v err=%v", called, res.Err, err)
	}
	// The synthetic failure changed no files. Restore the real OID so cleanup
	// can release the fixture's admitted lifetime lease through normal Complete.
	turn.before = original
	if record, err := turn.Complete(ctx); err != nil || record != nil {
		t.Fatalf("synthetic failure cleanup: record=%+v err=%v", record, err)
	}
}

func TestCompletionRepinsOriginalBeforeAfterPartialCapture(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writeCheckpointFixture(t, filepath.Join(home, "original.txt"), "original\r\n")
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("complete-pin-retry", "edit")
	runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(home).Spec()), `{"path":"original.txt","content":"changed"}`)
	before := turn.before
	advanced, err := m.captureSnapshot(ctx, []string{"original.txt"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.git(ctx, "update-ref", turn.active.root()+"/before", advanced); err != nil {
		t.Fatal(err)
	}
	turn.beforePinned = false
	// Observe the active root at the real commit point, before normal cleanup
	// retires it. Finalization must repair even if no further tool was called.
	err = turn.barrier.Complete(ctx, func(ctx context.Context) (bool, error) {
		committed, err := turn.commitCheckpoint(ctx)
		if err != nil {
			return committed, err
		}
		pin, err := m.git(ctx, "rev-parse", turn.active.root()+"/before")
		if err != nil || pin != before+"\n" {
			t.Fatalf("completion used an unpinned original snapshot: pin=%q before=%s err=%v", pin, before, err)
		}
		return committed, nil
	}, turn.releaseActivePins)
	if err != nil {
		t.Fatal(err)
	}
	record, err := turn.Complete(ctx)
	if err != nil || record == nil {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "original.txt"), []byte("original\r\n"))
}

// Compare the same 64 explicit create-file calls and 1 KiB file writes. Both
// variants establish the first live pin outside the timer; only reuse is
// disabled in the baseline, so all its ordinary capture guards still execute.
// Completion/provider work is outside this workload, not a full-task claim.
func BenchmarkAbsentScopeExpansionCreate64(b *testing.B) {
	for _, reuse := range []bool{false, true} {
		name := "capture_each"
		if reuse {
			name = "reuse_live_before"
		}
		b.Run(name, func(b *testing.B) {
			b.StopTimer()
			ctx := context.Background()
			m, err := Open(b.TempDir(), b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			body := bytes.Repeat([]byte("a"), 1024)
			args := make([]json.RawMessage, 64)
			paths := make([]string, len(args))
			for i := range args {
				paths[i] = fmt.Sprintf("created-%03d.bin", i)
				args[i] = json.RawMessage(fmt.Sprintf(`{"path":%q}`, paths[i]))
			}
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				b.StopTimer()
				for _, p := range paths {
					if err := os.Remove(filepath.Join(m.home, p)); err != nil && !os.IsNotExist(err) {
						b.Fatal(err)
					}
				}
				turn := m.NewTurn("synthetic", "create64")
				if err := turn.ensureLifetimeLease(ctx); err != nil {
					b.Fatal(err)
				}
				if err := turn.ensureBeforeForTool(ctx, "create_file", json.RawMessage(`{"path":"seed-absent.bin"}`)); err != nil {
					b.Fatal(err)
				}
				before := turn.before
				b.StartTimer()
				for i, a := range args {
					if !reuse {
						turn.beforePinned = false
					}
					if err := turn.ensureBeforeForTool(ctx, "create_file", a); err != nil {
						b.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(m.home, paths[i]), body, 0600); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				if turn.before != before || len(turn.scopeRoots) != 65 {
					b.Fatal("scope expansion changed the absent baseline")
				}
				record, err := turn.Complete(ctx)
				if err != nil || record == nil || len(record.Files) != len(paths) {
					b.Fatalf("record=%+v err=%v", record, err)
				}
			}
			b.ReportMetric(64, "creates/op")
		})
	}
}
