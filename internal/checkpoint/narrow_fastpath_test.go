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

func TestScopedSnapshotMinimalProof(t *testing.T) {
	for _, test := range []struct {
		name  string
		roots []string
		files []string
		whole bool
		want  bool
	}{
		{name: "exact-file", roots: []string{"file.bin"}, files: []string{"file.bin"}, want: true},
		{name: "create-delete", roots: []string{"new.bin", "old.bin"}, files: []string{"new.bin", "old.bin"}, want: true},
		{name: "D-F", roots: []string{"branch"}, files: []string{"branch", "branch/child.bin"}, want: true},
		{name: "directory-stays-directory", roots: []string{"branch"}, files: []string{"branch/child.bin"}},
		{name: "directory-prefix-is-not-file", roots: []string{"branch"}, files: []string{"branch-other.bin"}},
		{name: "untouched-backup", roots: []string{"book.xlsx", "book.xlsx.bak"}, files: []string{"book.xlsx"}},
		{name: "changed-backup", roots: []string{"book.xlsx", "book.xlsx.bak"}, files: []string{"book.xlsx", "book.xlsx.bak"}, want: true},
		{name: "whole-with-ignored-extra", roots: []string{"ignored.bak"}, files: []string{"ignored.bak"}, whole: true},
		{name: "unicode-and-CRLF-name", roots: []string{"żółć\r\n.bin"}, files: []string{"żółć\r\n.bin"}, want: true},
		{name: "lossy-UTF8-name", roots: []string{"bad-\xff.bin"}, files: []string{"bad-\xff.bin"}},
		{name: "exact-case-required", roots: []string{"File.bin"}, files: []string{"file.bin"}},
		{name: "workspace-root", roots: []string{"."}, files: []string{"file.bin"}},
		{name: "unknown-scope", files: []string{"file.bin"}},
		{name: "no-changes", roots: []string{"file.bin"}},
		{name: "invalid-diff-path", roots: []string{"file.bin"}, files: []string{"file.bin", "../escape"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			turn := &Turn{touched: true, wholeWorkspace: test.whole, scopeRoots: test.roots}
			if got := turn.snapshotsAlreadyMinimal(test.files); got != test.want {
				t.Fatalf("minimal proof=%v want=%v", got, test.want)
			}
		})
	}
	turn := &Turn{scopeRoots: []string{"file.bin"}}
	if turn.snapshotsAlreadyMinimal([]string{"file.bin"}) {
		t.Fatal("uncaptured turn accepted the proof")
	}
	turn.touched = true
	if turn.snapshotsAlreadyMinimal(make([]string, maxSnapshotFiles+1)) {
		t.Fatal("fastpath bypassed selection count limit")
	}
}

func TestRecordSnapshotsFastPathDoesNoIO(t *testing.T) {
	// A nil manager makes any fallback, Git, or workspace access fail. Only
	// trusted capture/scope/diff metadata is needed to return these identities.
	turn := &Turn{touched: true, scopeRoots: []string{"file.bin"}}
	before, after := strings.Repeat("a", 40), strings.Repeat("b", 40)
	gotBefore, gotAfter, err := turn.recordSnapshotsLocked(context.Background(), before, after, []string{"file.bin"})
	if err != nil || gotBefore != before || gotAfter != after {
		t.Fatalf("fastpath changed identities: before=%s after=%s err=%v", gotBefore, gotAfter, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	gotBefore, gotAfter, err = turn.recordSnapshotsLocked(ctx, before, after, []string{"file.bin"})
	if !errors.Is(err, context.Canceled) || gotBefore != "" || gotAfter != "" {
		t.Fatalf("fastpath ignored cancellation: before=%s after=%s err=%v", gotBefore, gotAfter, err)
	}
}

func TestFastpathNamesMustRoundtripRecordJSON(t *testing.T) {
	valid := []string{"żółć\r\n.bin"}
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	var restored []string
	if err := json.Unmarshal(data, &restored); err != nil || !reflect.DeepEqual(restored, valid) {
		t.Fatalf("valid UTF-8/CRLF name was not preserved: %v", err)
	}
	lossy := []string{"bad-\xff.bin"}
	data, err = json.Marshal(lossy)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &restored); err != nil || reflect.DeepEqual(restored, lossy) {
		t.Fatalf("invalid UTF-8 JSON risk was not reproduced: %v", err)
	}
	turn := &Turn{touched: true, scopeRoots: lossy}
	if turn.snapshotsAlreadyMinimal(lossy) {
		t.Fatal("fastpath accepted a name that Record.Files cannot persist")
	}
}

func runFastpathFixtureMutation(t *testing.T, turn *Turn, name string, args map[string]string, mutate func() error) {
	t.Helper()
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	spec := turn.Wrap(tools.Tool{Name: name, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{}, mutate()
	}})
	runSnapshotTool(t, spec, string(data))
}

func assertFastpathSnapshot(t *testing.T, m *Manager, commit string, bodies map[string][]byte) {
	t.Helper()
	want := make(map[string]string, len(bodies))
	for path, body := range bodies {
		_, oid := expectedGitBlob(body)
		want[path] = "100644 blob " + oid
	}
	got := narrowRecordTreeEntries(t, m, commit)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("record tree retained unrelated entries or changed raw OIDs: got=%v want=%v", got, want)
	}
}

func TestScopedFastpathCreateDeleteUnicodeAndBinaryBytes(t *testing.T) {
	ctx := context.Background()
	m := newNarrowRecordTestManager(t)
	writeOversizedSnapshotFixture(t, m.home)
	// Contents may contain invalid UTF-8; only names enter Record.Files JSON.
	deletedBody := []byte{0xff, 0xfe, '\r', '\n', 0}
	createdBody := []byte{0, 0xfe, 0xff, '\r', '\n'}
	created, deleted := "żółć.bin", "deleted.bin"
	writeNarrowRecordFixture(t, m, deleted, deletedBody)
	turn := m.NewTurn("fast-create-delete", "synthetic mutation")
	runFastpathFixtureMutation(t, turn, "write_file", map[string]string{"path": created}, func() error {
		return os.WriteFile(filepath.Join(m.home, created), createdBody, 0o600)
	})
	runFastpathFixtureMutation(t, turn, "trash", map[string]string{"path": deleted}, func() error {
		return os.Remove(filepath.Join(m.home, deleted))
	})
	record, err := turn.Complete(ctx)
	if err != nil || record == nil {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if !turn.snapshotsAlreadyMinimal(record.Files) {
		t.Fatal("exact create/delete scopes did not prove minimal snapshots")
	}
	if record.Before != turn.before || record.After != turn.snapshotAfter {
		t.Fatal("already-minimal capture identities were changed")
	}
	assertFastpathSnapshot(t, m, record.Before, map[string][]byte{deleted: deletedBody})
	assertFastpathSnapshot(t, m, record.After, map[string][]byte{created: createdBody})
	data, err := os.ReadFile(m.meta)
	if err != nil {
		t.Fatal(err)
	}
	var persisted []Record
	if err := json.Unmarshal(data, &persisted); err != nil || len(persisted) != 1 || !reflect.DeepEqual(persisted[0].Files, record.Files) {
		t.Fatalf("Unicode file selection changed in metadata: %v", err)
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(m.home, deleted), deletedBody)
	if _, err := os.Stat(filepath.Join(m.home, created)); !os.IsNotExist(err) {
		t.Fatalf("created file did not return to absence: %v", err)
	}
	if _, err := m.Redo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(m.home, created), createdBody)
}

func TestScopedFastpathFileDirectoryChanges(t *testing.T) {
	for _, directoryBefore := range []bool{false, true} {
		name := "file-to-directory"
		if directoryBefore {
			name = "directory-to-file"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			m := newNarrowRecordTestManager(t)
			writeOversizedSnapshotFixture(t, m.home)
			writeNarrowRecordFixture(t, m, ".gitignore", []byte("*.bak\n"))
			fileBody, childBody, backupBody := []byte("root-file\r\n"), []byte{0, 255, 1}, []byte("explicit ignored child")
			beforeBodies := map[string][]byte{"branch": fileBody}
			afterBodies := map[string][]byte{"branch/child.bin": childBody}
			if directoryBefore {
				beforeBodies = map[string][]byte{"branch/child.bin": childBody, "branch/ignored.bak": backupBody}
				afterBodies = map[string][]byte{"branch": fileBody}
			}
			for path, body := range beforeBodies {
				writeNarrowRecordFixture(t, m, path, body)
			}
			branch := filepath.Join(m.home, "branch")
			if !within(m.home, branch) {
				t.Fatal("fixture branch escapes workspace")
			}
			turn := m.NewTurn(name, "synthetic D/F mutation")
			runFastpathFixtureMutation(t, turn, "trash", map[string]string{"path": "branch"}, func() error {
				if directoryBefore {
					return os.RemoveAll(branch) // Fixed, checked fixture directory only.
				}
				return os.Remove(branch)
			})
			if !directoryBefore {
				runFastpathFixtureMutation(t, turn, "make_dir", map[string]string{"path": "branch"}, func() error {
					return os.MkdirAll(branch, 0o700)
				})
			}
			for path, body := range afterBodies {
				runFastpathFixtureMutation(t, turn, "write_file", map[string]string{"path": path}, func() error {
					return os.WriteFile(filepath.Join(m.home, filepath.FromSlash(path)), body, 0o600)
				})
			}
			record, err := turn.Complete(ctx)
			if err != nil || record == nil {
				t.Fatalf("D/F record=%+v err=%v", record, err)
			}
			if !turn.snapshotsAlreadyMinimal(record.Files) || record.Before != turn.before || record.After != turn.snapshotAfter {
				t.Fatal("D/F proof did not preserve its already-minimal captures")
			}
			assertFastpathSnapshot(t, m, record.Before, beforeBodies)
			assertFastpathSnapshot(t, m, record.After, afterBodies)
		})
	}
}

func TestScopedSnapshotFastpathFallsBackForUntouchedDirectoryAndBackup(t *testing.T) {
	for _, office := range []bool{false, true} {
		name, tool := "directory-scope", "read_zip"
		changed, untouched := "branch/edit.bin", "branch/ignored.bak"
		args := map[string]string{"action": "extract", "target_dir": "branch"}
		if office {
			name, tool = "untouched-office-backup", "edit_xlsx"
			changed, untouched = "book.xlsx", "book.xlsx.bak"
			args = map[string]string{"path": changed}
		}
		t.Run(name, func(t *testing.T) {
			m := newNarrowRecordTestManager(t)
			writeOversizedSnapshotFixture(t, m.home)
			writeNarrowRecordFixture(t, m, ".gitignore", []byte("*.bak\n"))
			beforeBody, afterBody := []byte("before\r\n"), []byte{0xff, 0, '\r', '\n'}
			backupBody := []byte("untouched ignored backup must not be retained")
			writeNarrowRecordFixture(t, m, changed, beforeBody)
			writeNarrowRecordFixture(t, m, untouched, backupBody)
			turn := m.NewTurn(name, "synthetic mutation")
			runFastpathFixtureMutation(t, turn, tool, args, func() error {
				return os.WriteFile(filepath.Join(m.home, filepath.FromSlash(changed)), afterBody, 0o600)
			})
			record, err := turn.Complete(context.Background())
			if err != nil || record == nil {
				t.Fatalf("fallback record=%+v err=%v", record, err)
			}
			if turn.snapshotsAlreadyMinimal(record.Files) || record.Before == turn.before || record.After == turn.snapshotAfter {
				t.Fatal("uncertain scope incorrectly retained its broad scoped captures")
			}
			assertFastpathSnapshot(t, m, record.Before, map[string][]byte{changed: beforeBody})
			assertFastpathSnapshot(t, m, record.After, map[string][]byte{changed: afterBody})
			assertFileBytes(t, filepath.Join(m.home, filepath.FromSlash(untouched)), backupBody)
		})
	}
}

func TestScopedSnapshotFastpathFallsBackForWholeCaptureWithIgnoredExtra(t *testing.T) {
	m := newNarrowRecordTestManager(t)
	writeNarrowRecordFixture(t, m, ".gitignore", []byte("*.bak\n"))
	writeNarrowRecordFixture(t, m, "unrelated.bin", []byte("whole capture only"))
	beforeBody, afterBody := []byte("ignored before\r\n"), []byte("ignored after\r\n")
	writeNarrowRecordFixture(t, m, "ignored.bak", beforeBody)
	turn := m.NewTurn("whole-and-ignored", "synthetic mutation")
	if err := turn.ensureBefore(context.Background()); err != nil {
		t.Fatal(err)
	}
	runFastpathFixtureMutation(t, turn, "write_file", map[string]string{"path": "ignored.bak"}, func() error {
		return os.WriteFile(filepath.Join(m.home, "ignored.bak"), afterBody, 0o600)
	})
	record, err := turn.Complete(context.Background())
	if err != nil || record == nil {
		t.Fatalf("whole/ignored record=%+v err=%v", record, err)
	}
	if turn.snapshotsAlreadyMinimal(record.Files) || record.Before == turn.before || record.After == turn.snapshotAfter {
		t.Fatal("whole capture bypassed narrowing because its ignored extra changed")
	}
	assertFastpathSnapshot(t, m, record.Before, map[string][]byte{"ignored.bak": beforeBody})
	assertFastpathSnapshot(t, m, record.After, map[string][]byte{"ignored.bak": afterBody})
}

func BenchmarkRecordSnapshotsFastPath64(b *testing.B) {
	files := make([]string, 64)
	for i := range files {
		files[i] = strings.Repeat("a", i+1) + ".bin"
	}
	turn := &Turn{touched: true, scopeRoots: files}
	before, after := strings.Repeat("a", 40), strings.Repeat("b", 40)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gotBefore, gotAfter, err := turn.recordSnapshotsLocked(ctx, before, after, files)
		if err != nil || gotBefore != before || gotAfter != after {
			b.Fatalf("fastpath failed: %v", err)
		}
	}
	b.ReportMetric(64, "files/op")
}
