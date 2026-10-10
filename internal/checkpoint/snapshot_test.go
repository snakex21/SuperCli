package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/tools"
)

func openSnapshotTestManager(t *testing.T, home string) *Manager {
	t.Helper()
	m, err := Open(home, t.TempDir())
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func runSnapshotTool(t *testing.T, spec tools.Tool, args string) {
	t.Helper()
	result, err := spec.Fn(context.Background(), json.RawMessage(args))
	if err != nil || result.Err != nil {
		t.Fatalf("tool failed: err=%v result=%v", err, result.Err)
	}
}

func writeOversizedSnapshotFixture(t *testing.T, home string) {
	t.Helper()
	f, err := os.Create(filepath.Join(home, "unrelated-large-archive.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(maxSnapshotFileBytes + 1); err != nil {
		t.Fatal(err)
	}
}

func TestScopedTurnAvoidsUnrelatedLargeFilesAndPreservesFirstBefore(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "original-a")
	writeCheckpointFixture(t, filepath.Join(home, "b.txt"), "original-b")
	writeOversizedSnapshotFixture(t, home)
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("scoped", "edit two files")
	write := turn.Wrap(tools.NewWriteFile(home).Spec())
	runSnapshotTool(t, write, `{"path":"a.txt","content":"intermediate-a"}`)
	runSnapshotTool(t, write, `{"path":"b.txt","content":"final-b"}`)
	runSnapshotTool(t, write, `{"path":"a.txt","content":"final-a"}`)
	runSnapshotTool(t, write, `{"path":"new.txt","content":"first-new"}`)
	runSnapshotTool(t, write, `{"path":"new.txt","content":"final-new"}`)
	record, err := turn.Complete(ctx)
	if err != nil || record == nil {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if !record.RawBytes || !reflect.DeepEqual(record.Files, []string{"a.txt", "b.txt", "new.txt"}) {
		t.Fatalf("scope=%v", record.Files)
	}
	for _, commit := range []string{record.Before, record.After} {
		paths, err := m.git(ctx, "ls-tree", "-r", "--name-only", commit)
		if err != nil || strings.Contains(paths, "archive") {
			t.Fatalf("unrelated archive captured: %q err=%v", paths, err)
		}
	}
	var bytes int64
	if err := filepath.WalkDir(m.repo, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() {
			info, err := e.Info()
			if err != nil {
				return err
			}
			bytes += info.Size()
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if bytes > 128<<10 {
		t.Fatalf("small edits grew checkpoint to %d bytes", bytes)
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("original-a"))
	assertFileBytes(t, filepath.Join(home, "b.txt"), []byte("original-b"))
	if _, err := os.Stat(filepath.Join(home, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("created path lost its absent baseline: %v", err)
	}
	if _, err := m.Redo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("final-a"))
	assertFileBytes(t, filepath.Join(home, "new.txt"), []byte("final-new"))
}

func TestMixedWholeAndScopedTurnPreservesExplicitIgnoredPaths(t *testing.T) {
	for _, wholeFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "scope-before-command", true: "command-before-scope"}[wholeFirst], func(t *testing.T) {
			ctx := context.Background()
			home := t.TempDir()
			writeCheckpointFixture(t, filepath.Join(home, ".gitignore"), "*.bak\n")
			writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "original")
			writeCheckpointFixture(t, filepath.Join(home, "ignored.bak"), "original-ignored")
			m := openSnapshotTestManager(t, home)
			turn := m.NewTurn("mixed-ignored", "command and ignored path")
			if wholeFirst {
				if err := turn.ensureBefore(ctx); err != nil {
					t.Fatal(err)
				}
				// A later file tool must not capture this already-created file
				// as existing in the original whole-workspace baseline.
				writeCheckpointFixture(t, filepath.Join(home, "new.txt"), "command-created")
			}
			write := turn.Wrap(tools.NewWriteFile(home).Spec())
			runSnapshotTool(t, write, `{"path":"ignored.bak","content":"changed-ignored"}`)
			runSnapshotTool(t, write, `{"path":"new.txt","content":"final-new"}`)
			if !wholeFirst {
				if err := turn.ensureBefore(ctx); err != nil {
					t.Fatal(err)
				}
			}
			writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "changed")
			record, err := turn.Complete(ctx)
			if err != nil || record == nil {
				t.Fatalf("record=%+v err=%v", record, err)
			}
			if _, err := m.Undo(ctx, record.ID); err != nil {
				t.Fatal(err)
			}
			assertFileBytes(t, filepath.Join(home, "ignored.bak"), []byte("original-ignored"))
			assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("original"))
			if _, err := os.Stat(filepath.Join(home, "new.txt")); !os.IsNotExist(err) {
				t.Fatalf("new path became part of the before-state: %v", err)
			}
			if _, err := m.Redo(ctx, record.ID); err != nil {
				t.Fatal(err)
			}
			assertFileBytes(t, filepath.Join(home, "ignored.bak"), []byte("changed-ignored"))
		})
	}
}

func TestLegacyCRLFCheckpointRefusesByteLossWithoutRunningFilters(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writeCheckpointFixture(t, filepath.Join(home, ".gitattributes"), "*.txt text eol=lf\n")
	writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "before\r\n")
	m := openSnapshotTestManager(t, home)
	if err := m.ensureRepoLocked(ctx); err != nil {
		t.Fatal(err)
	}
	captureLegacy := func() string {
		t.Helper()
		if _, err := m.git(ctx, "add", "-A", "--", "."); err != nil {
			t.Fatal(err)
		}
		tree, err := m.git(ctx, "write-tree")
		if err != nil {
			t.Fatal(err)
		}
		commit, err := m.commitTreeLocked(ctx, strings.TrimSpace(tree))
		if err != nil {
			t.Fatal(err)
		}
		return commit
	}
	before := captureLegacy()
	writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "after\r\n")
	after := captureLegacy()
	record := Record{ID: "legacy-crlf", SessionID: "legacy", Before: before, After: after, Files: []string{"a.txt"}}
	if err := m.append(record); err != nil {
		t.Fatal(err)
	}
	// A required external filter would fail if the comparison executed it.
	if _, err := m.git(ctx, "config", "filter.fixture.clean", "false"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.git(ctx, "config", "filter.fixture.required", "true"); err != nil {
		t.Fatal(err)
	}
	writeCheckpointFixture(t, filepath.Join(home, ".gitattributes"), "*.txt text eol=lf filter=fixture\n")
	result, err := m.Undo(ctx, record.ID)
	if !errors.Is(err, ErrLegacyByteLoss) || len(result.Conflicts) != 1 {
		t.Fatalf("legacy byte loss not explained: result=%+v err=%v", result, err)
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("after\r\n"))
	if m.Latest("legacy").Undone {
		t.Fatal("refused legacy restore changed metadata")
	}
}

func TestLegacyUnfilteredBinaryCheckpointRemainsUsable(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	file := filepath.Join(home, "a.bin")
	beforeBytes, afterBytes := []byte{0, 255, 0, 1}, []byte{128, 0, 254, 2}
	if err := os.WriteFile(file, beforeBytes, 0644); err != nil {
		t.Fatal(err)
	}
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("legacy-binary", "edit")
	if err := turn.ensureBefore(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, afterBytes, 0644); err != nil {
		t.Fatal(err)
	}
	record, err := turn.Complete(ctx)
	if err != nil || record == nil {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	m.records[0].RawBytes = false // Same byte-preserving blob format as old git add.
	if err := m.saveLocked(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, file, beforeBytes)
	if _, err := m.Redo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, file, afterBytes)
}

func TestScopedToWholeTurnKeepsEarliestModifiedAndAbsentPaths(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "original-a")
	writeCheckpointFixture(t, filepath.Join(home, "b.txt"), "original-b")
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("mixed", "files then command")
	write := turn.Wrap(tools.NewWriteFile(home).Spec())
	runSnapshotTool(t, write, `{"path":"a.txt","content":"final-a"}`)
	runSnapshotTool(t, write, `{"path":"new.txt","content":"new-content"}`)
	if err := turn.ensureBeforeForTool(ctx, "ctx_execute", json.RawMessage(`{"command":["custom-command"]}`)); err != nil {
		t.Fatal(err)
	}
	writeCheckpointFixture(t, filepath.Join(home, "b.txt"), "final-b")
	record, err := turn.Complete(ctx)
	if err != nil || record == nil {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("original-a"))
	assertFileBytes(t, filepath.Join(home, "b.txt"), []byte("original-b"))
	if _, err := os.Stat(filepath.Join(home, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("full expansion captured an already-created file: %v", err)
	}
	if _, err := m.Redo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "new.txt"), []byte("new-content"))
}

func TestScopedDirectoryExpansionAndMoveIntoExistingFolder(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writeCheckpointFixture(t, filepath.Join(home, "src", "a.txt"), "original")
	writeCheckpointFixture(t, filepath.Join(home, "src", "ignored.bak"), "backup-bytes")
	writeCheckpointFixture(t, filepath.Join(home, ".gitignore"), "*.bak\n")
	writeCheckpointFixture(t, filepath.Join(home, "destination", "unrelated.txt"), "keep")
	writeOversizedSnapshotFixture(t, home)
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("move", "edit then move folder")
	runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(home).Spec()), `{"path":"src/a.txt","content":"changed"}`)
	runSnapshotTool(t, turn.Wrap(tools.NewMove(home).Spec()), `{"src":"src","dest":"destination"}`)
	record, err := turn.Complete(ctx)
	if err != nil || record == nil {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "src", "a.txt"), []byte("original"))
	assertFileBytes(t, filepath.Join(home, "src", "ignored.bak"), []byte("backup-bytes"))
	assertFileBytes(t, filepath.Join(home, "destination", "unrelated.txt"), []byte("keep"))
	if _, err := m.Redo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "destination", "src", "a.txt"), []byte("changed"))
	assertFileBytes(t, filepath.Join(home, "destination", "src", "ignored.bak"), []byte("backup-bytes"))
}

func TestFullCaptureRejectsLargeFileBeforeMutationOrBlobWrites(t *testing.T) {
	home := t.TempDir()
	writeOversizedSnapshotFixture(t, home)
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("limit", "unknown mutation")
	t.Cleanup(func() {
		record, err := turn.Complete(context.Background())
		if err != nil || record != nil {
			t.Errorf("rejected mutation cleanup: record=%+v err=%v", record, err)
		}
		if pendingOwnerCount(t, m) != 0 {
			t.Error("rejected mutation retained its lifetime lease")
		}
	})
	called := false
	spec := turn.Wrap(tools.Tool{Name: "ctx_execute", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		called = true
		return tools.Result{}, nil
	}})
	result, _ := spec.Fn(context.Background(), json.RawMessage(`{"command":["custom-command"]}`))
	if !errors.Is(result.Err, ErrSnapshotLimit) || called || turn.touched {
		t.Fatalf("mutation ran or limit lost: err=%v called=%v touched=%v", result.Err, called, turn.touched)
	}
	entries, err := os.ReadDir(filepath.Join(m.repo, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "info" && entry.Name() != "pack" {
			t.Fatalf("rejected capture wrote an object: %s", entry.Name())
		}
	}
}

func TestRawSnapshotsPreserveCRLFAndDoNotRunCleanFilters(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writeCheckpointFixture(t, filepath.Join(home, ".gitattributes"), "*.txt text eol=lf filter=fixture\n")
	writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "before\r\n")
	m := openSnapshotTestManager(t, home)
	if err := m.ensureRepoLocked(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.git(ctx, "config", "filter.fixture.clean", "printf filtered"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.git(ctx, "config", "filter.fixture.required", "true"); err != nil {
		t.Fatal(err)
	}
	turn := m.NewTurn("bytes", "change raw text")
	if err := turn.ensureBefore(ctx); err != nil {
		t.Fatal(err)
	}
	writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "after\r\n")
	record, err := turn.Complete(ctx)
	if err != nil || record == nil {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("before\r\n"))
	if _, err := m.Redo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("after\r\n"))
}

func TestRecordRefsKeepHistoricalUndoAfterGitPrune(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "original")
	m := openSnapshotTestManager(t, home)
	var first, second *Record
	for i, content := range []string{"one", "two"} {
		turn := m.NewTurn("retained", "edit")
		runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(home).Spec()), `{"path":"a.txt","content":"`+content+`"}`)
		record, err := turn.Complete(ctx)
		if err != nil || record == nil {
			t.Fatalf("record=%+v err=%v", record, err)
		}
		if i == 0 {
			first = record
		} else {
			second = record
		}
	}
	if _, err := m.git(ctx, "prune", "--expire=now"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Undo(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Undo(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("original"))
	if _, err := m.Redo(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Redo(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("two"))
}

func TestWebDownloadCheckpointIncludesDestinationOnly(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	writeOversizedSnapshotFixture(t, home)
	writeCheckpointFixture(t, filepath.Join(home, "download.bin"), "before")
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("download", "download replacement")
	download := turn.Wrap(tools.Tool{Name: "web_download", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{}, os.WriteFile(filepath.Join(home, "download.bin"), []byte{0, 255, 0, 128}, 0600)
	}})
	runSnapshotTool(t, download, `{"path":"download.bin"}`)
	record, err := turn.Complete(ctx)
	if err != nil || record == nil || !reflect.DeepEqual(record.Files, []string{"download.bin"}) {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "download.bin"), []byte("before"))
	if _, err := m.Redo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(home, "download.bin"), []byte{0, 255, 0, 128})
}
