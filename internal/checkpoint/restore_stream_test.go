package checkpoint

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"supercli/internal/system/childproc"
	"supercli/internal/tools"
)

func writeStreamingBinaryFixture(file string, size int64) ([32]byte, error) {
	output, err := os.Create(file)
	if err != nil {
		return [32]byte{}, err
	}
	defer output.Close()
	block := make([]byte, 64<<10)
	state := uint32(0x983749ab)
	for i := range block {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		block[i] = byte(state)
	}
	hash := sha256.New()
	writer := io.MultiWriter(output, hash)
	for remaining := size; remaining > 0; {
		n := int64(len(block))
		if remaining < n {
			n = remaining
		}
		if _, err := writer.Write(block[:n]); err != nil {
			return [32]byte{}, err
		}
		remaining -= n
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result, output.Close()
}

func streamingFixtureHash(file string) ([32]byte, error) {
	input, err := os.Open(file)
	if err != nil {
		return [32]byte{}, err
	}
	defer input.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		return [32]byte{}, err
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func assertNoRestoreTemporaries(t *testing.T, home string) {
	t.Helper()
	if err := filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".supercli-restore-") {
			t.Errorf("restore temporary remains at %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStreamingRestoreLargeBinaryHasBoundedHeapAndExactBytes(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	m := openSnapshotTestManager(t, home)
	file := filepath.Join(home, "large.bin")
	turn := m.NewTurn("large", "create large binary")
	if err := turn.ensureBeforeForTool(ctx, "write_file", json.RawMessage(`{"path":"large.bin"}`)); err != nil {
		t.Fatal(err)
	}
	const size = int64(32 << 20)
	want, err := writeStreamingBinaryFixture(file, size)
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
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := m.Redo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("restoring %d bytes allocated %d Go heap bytes; expected a fixed-size stream", size, allocated)
	}
	got, err := streamingFixtureHash(file)
	if err != nil || got != want {
		t.Fatalf("restored binary differs: got=%x want=%x err=%v", got, want, err)
	}
	assertNoRestoreTemporaries(t, home)
}

func makeTwoFileRestoreFixture(t *testing.T) (*Manager, *Record, string) {
	t.Helper()
	home := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		writeCheckpointFixture(t, filepath.Join(home, name), "before-"+name)
	}
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("rollback", "two file edit")
	write := turn.Wrap(tools.NewWriteFile(home).Spec())
	for _, name := range []string{"a.txt", "b.txt"} {
		runSnapshotTool(t, write, `{"path":"`+name+`","content":"after-`+name+`"}`)
	}
	record, err := turn.Complete(context.Background())
	if err != nil || record == nil {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	return m, record, home
}

func TestStreamingRestoreRollsBackWhenSecondPromotionFails(t *testing.T) {
	m, record, home := makeTwoFileRestoreFixture(t)
	calls := 0
	rename := func(from, to string) error {
		calls++
		if calls == 2 {
			return errors.New("fixture destination is locked")
		}
		return os.Rename(from, to)
	}
	if _, err := m.restoreWithRename(context.Background(), record.ID, false, rename); err == nil {
		t.Fatal("expected promotion failure")
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		assertFileBytes(t, filepath.Join(home, name), []byte("after-"+name))
	}
	if calls < 3 || m.Latest("rollback").Undone {
		t.Fatalf("rollback not completed: rename calls=%d record=%+v", calls, m.Latest("rollback"))
	}
	assertNoRestoreTemporaries(t, home)
}

func TestStreamingRestoreRollsBackWhenMetadataSaveFails(t *testing.T) {
	m, record, home := makeTwoFileRestoreFixture(t)
	calls := 0
	rename := func(from, to string) error {
		calls++
		if calls == 2 {
			// Admission now reloads/validates metadata. Inject the write failure only
			// after actual promotions, so this still exercises rollback, not refusal.
			if err := os.Mkdir(m.meta+".tmp", 0700); err != nil {
				return err
			}
		}
		return os.Rename(from, to)
	}
	if _, err := m.restoreWithRename(context.Background(), record.ID, false, rename); err == nil {
		t.Fatal("expected metadata persistence failure")
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		assertFileBytes(t, filepath.Join(home, name), []byte("after-"+name))
	}
	latest := m.Latest("rollback")
	if calls < 4 || latest == nil || latest.Undone {
		t.Fatalf("failed persistence did not fully roll back: calls=%d record=%+v", calls, latest)
	}
	assertNoRestoreTemporaries(t, home)
	if _, err := os.Stat(m.meta + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("failed metadata temporary remains: %v", err)
	}
}

func TestStreamingRestoreRefusesLaterSymlinkAncestor(t *testing.T) {
	ctx := context.Background()
	home, outside := t.TempDir(), t.TempDir()
	file := filepath.Join(home, "nested", "a.txt")
	writeCheckpointFixture(t, file, "before")
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("symlink", "edit nested file")
	runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(home).Spec()), `{"path":"nested/a.txt","content":"after"}`)
	record, err := turn.Complete(ctx)
	if err != nil || record == nil {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	writeCheckpointFixture(t, filepath.Join(outside, "a.txt"), "after")
	nested := filepath.Join(home, "nested")
	if err := os.Rename(nested, filepath.Join(home, "saved-nested")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, nested); err != nil {
		if runtime.GOOS != "windows" {
			t.Skipf("symlinks unavailable: %v", err)
		}
		cmd := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", nested, outside)
		childproc.HideWindow(cmd)
		if err := cmd.Run(); err != nil {
			t.Skipf("symlinks and junctions unavailable: %v", err)
		}
	}
	defer os.Remove(nested)
	result, err := m.Undo(ctx, record.ID)
	if err == nil || len(result.Conflicts) != 1 {
		t.Fatalf("later path redirection was accepted: result=%+v err=%v", result, err)
	}
	assertFileBytes(t, filepath.Join(outside, "a.txt"), []byte("after"))
	assertFileBytes(t, filepath.Join(home, "saved-nested", "a.txt"), []byte("after"))
	if m.Latest("symlink").Undone {
		t.Fatal("refused redirected restore changed metadata")
	}
	assertNoRestoreTemporaries(t, outside)
	assertNoRestoreTemporaries(t, filepath.Join(home, "saved-nested"))
}

type snapshotSizedInfo struct {
	os.FileInfo
	bytes int64
}

func (i snapshotSizedInfo) Size() int64 { return i.bytes }

func TestExpandedSnapshotCountsUnionWhenPathsAlreadyExistInBase(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	for _, name := range []string{"a", "b", "c", "d"} {
		writeCheckpointFixture(t, filepath.Join(home, name), "base")
	}
	m := openSnapshotTestManager(t, home)
	base, err := m.capture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	files := []snapshotFile{}
	for _, name := range []string{"a", "b", "c", "d"} {
		info, err := os.Stat(filepath.Join(home, name))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, snapshotFile{path: name, info: snapshotSizedInfo{FileInfo: info, bytes: maxSnapshotFileBytes}})
	}
	// Four replacement entries exactly reach the aggregate limit; the old
	// versions of those same paths must not consume the budget a second time.
	if err := m.checkExpandedSnapshot(ctx, base, files); err != nil {
		t.Fatalf("replacement entries were double-counted: %v", err)
	}
}

func BenchmarkCheckpointStreamingRestore(b *testing.B) {
	for _, size := range []int64{1 << 20, 64 << 20} {
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) {
			ctx := context.Background()
			home, data := b.TempDir(), b.TempDir()
			m, err := Open(home, data)
			if errors.Is(err, ErrUnavailable) {
				b.Skip(err)
			}
			if err != nil {
				b.Fatal(err)
			}
			turn := m.NewTurn("benchmark", "binary create")
			if err := turn.ensureBeforeForTool(ctx, "write_file", json.RawMessage(`{"path":"binary.bin"}`)); err != nil {
				b.Fatal(err)
			}
			if _, err := writeStreamingBinaryFixture(filepath.Join(home, "binary.bin"), size); err != nil {
				b.Fatal(err)
			}
			record, err := turn.Complete(ctx)
			if err != nil || record == nil {
				b.Fatalf("record=%+v err=%v", record, err)
			}
			if _, err := m.Undo(ctx, record.ID); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(size * 2) // One streamed write and one current-state hash.
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := m.Redo(ctx, record.ID); err != nil {
					b.Fatal(err)
				}
				if _, err := m.Undo(ctx, record.ID); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
