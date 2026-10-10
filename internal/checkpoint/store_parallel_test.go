package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openParallelStoreManagers(t *testing.T) (*Manager, *Manager) {
	t.Helper()
	home, data := t.TempDir(), t.TempDir()
	a, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(home, data)
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

func readParallelStoreRecords(t *testing.T, m *Manager) map[string]Record {
	t.Helper()
	data, err := os.ReadFile(m.meta)
	if err != nil {
		t.Fatal(err)
	}
	var records []Record
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]Record, len(records))
	for _, record := range records {
		if _, duplicate := byID[record.ID]; duplicate {
			t.Fatalf("duplicate persisted checkpoint %q", record.ID)
		}
		byID[record.ID] = record
	}
	return byID
}

func recordParallelStoreFileChange(t *testing.T, m *Manager, id, session, path string, seq int, before, after []byte) Record {
	t.Helper()
	ctx := context.Background()
	full := filepath.Join(m.home, path)
	if err := os.WriteFile(full, before, 0644); err != nil {
		t.Fatal(err)
	}
	beforeCommit, err := m.captureSnapshot(ctx, []string{path}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, after, 0644); err != nil {
		t.Fatal(err)
	}
	afterCommit, err := m.captureSnapshot(ctx, []string{path}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	record := Record{
		ID: id, SessionID: session, UserSeq: seq,
		Before: beforeCommit, After: afterCommit, Files: []string{path},
		Changes: []FileChange{{Path: path, Kind: "modified"}}, RawBytes: true,
		CreatedAt: time.Unix(1000+int64(seq), 0).UTC(),
	}
	if err := m.append(record); err != nil {
		t.Fatal(err)
	}
	return record
}

// Both callbacks start at the same barrier; completion channels replace timing
// assumptions, sleep, and progress polling. A goroutine never calls t.Fatal.
func runParallelStoreTransactions(t *testing.T, first, second func() error) {
	t.Helper()
	start := make(chan struct{})
	finished := make(chan error, 2)
	for _, transaction := range []func() error{first, second} {
		go func(fn func() error) {
			<-start
			finished <- fn()
		}(transaction)
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}

func TestStoreParallelAppendKeepsBothManagersRecords(t *testing.T) {
	a, b := openParallelStoreManagers(t)
	const perManager = 24
	appendMany := func(m *Manager, prefix string) func() error {
		return func() error {
			for i := 0; i < perManager; i++ {
				record := Record{ID: fmt.Sprintf("%s-%02d", prefix, i), SessionID: prefix, UserSeq: i + 1}
				if err := m.append(record); err != nil {
					return fmt.Errorf("append %s: %w", record.ID, err)
				}
			}
			return nil
		}
	}
	runParallelStoreTransactions(t, appendMany(a, "first"), appendMany(b, "second"))
	records := readParallelStoreRecords(t, a)
	if len(records) != perManager*2 {
		t.Fatalf("concurrent append lost checkpoints: got %d, want %d", len(records), perManager*2)
	}
	for _, prefix := range []string{"first", "second"} {
		for i := 0; i < perManager; i++ {
			id := fmt.Sprintf("%s-%02d", prefix, i)
			record, exists := records[id]
			if !exists || record.SessionID != prefix || record.UserSeq != i+1 {
				t.Fatalf("missing or changed checkpoint %q: %+v", id, record)
			}
		}
	}
}

func TestStoreParallelForgetAndAppendPreserveOtherSession(t *testing.T) {
	a, b := openParallelStoreManagers(t)
	for _, record := range []Record{
		{ID: "old-first", SessionID: "discard", UserSeq: 1},
		{ID: "old-second", SessionID: "discard", UserSeq: 2},
		{ID: "retained", SessionID: "keep", UserSeq: 1},
	} {
		if err := a.append(record); err != nil {
			t.Fatal(err)
		}
	}
	runParallelStoreTransactions(t,
		func() error { return a.ForgetFrom("discard", 1) },
		func() error {
			for i := 0; i < 12; i++ {
				if err := b.append(Record{ID: fmt.Sprintf("new-%d", i), SessionID: "keep", UserSeq: i + 2}); err != nil {
					return err
				}
			}
			return nil
		},
	)
	records := readParallelStoreRecords(t, a)
	if len(records) != 13 {
		t.Fatalf("concurrent forget lost or resurrected records: %+v", records)
	}
	if _, exists := records["retained"]; !exists {
		t.Fatal("forget removed an unrelated session checkpoint")
	}
	for _, id := range []string{"old-first", "old-second"} {
		if _, exists := records[id]; exists {
			t.Fatalf("append resurrected forgotten checkpoint %q", id)
		}
	}
	for i := 0; i < 12; i++ {
		if _, exists := records[fmt.Sprintf("new-%d", i)]; !exists {
			t.Fatalf("forget lost concurrent append %d", i)
		}
	}
}

func TestStoreSecondManagerUndoReloadsRecordAndFlags(t *testing.T) {
	a, b := openParallelStoreManagers(t)
	before := []byte{'B', '\r', '\n', 0, 0xff}
	after := []byte{'A', '\r', '\n', 0, 0xfe}
	record := recordParallelStoreFileChange(t, a, "binary", "files", "binary.bin", 1, before, after)
	ctx := context.Background()
	if _, err := b.Undo(ctx, record.ID); err != nil {
		t.Fatalf("second manager could not undo newly appended record: %v", err)
	}
	assertFileBytes(t, filepath.Join(a.home, "binary.bin"), before)
	if err := a.append(Record{ID: "unrelated", SessionID: "other", UserSeq: 1}); err != nil {
		t.Fatal(err)
	}
	if current := readParallelStoreRecords(t, a)[record.ID]; !current.Undone {
		t.Fatal("stale append overwrote the second manager's undo flag")
	}
	if _, err := a.Redo(ctx, record.ID); err != nil {
		t.Fatalf("first manager could not redo the second manager's undo: %v", err)
	}
	assertFileBytes(t, filepath.Join(a.home, "binary.bin"), after)
	if err := b.ForgetFrom("other", 1); err != nil {
		t.Fatal(err)
	}
	records := readParallelStoreRecords(t, a)
	if len(records) != 1 || records[record.ID].Undone {
		t.Fatalf("stale forget overwrote the first manager's redo flag: %+v", records)
	}
	if _, err := b.Undo(ctx, record.ID); err != nil {
		t.Fatalf("second manager retained a stale Undone flag: %v", err)
	}
	assertFileBytes(t, filepath.Join(a.home, "binary.bin"), before)
}

func TestStoreParallelUndoAndAppendKeepUndoneFlag(t *testing.T) {
	a, b := openParallelStoreManagers(t)
	record := recordParallelStoreFileChange(t, a, "restored", "files", "app.txt", 1, []byte("before"), []byte("after"))
	runParallelStoreTransactions(t,
		func() error {
			_, err := a.Undo(context.Background(), record.ID)
			return err
		},
		func() error { return b.append(Record{ID: "concurrent", SessionID: "other", UserSeq: 1}) },
	)
	records := readParallelStoreRecords(t, a)
	if len(records) != 2 || !records[record.ID].Undone {
		t.Fatalf("concurrent append/undo lost metadata or undo flag: %+v", records)
	}
	if _, exists := records["concurrent"]; !exists {
		t.Fatal("undo lost another manager's concurrent append")
	}
	assertFileBytes(t, filepath.Join(a.home, "app.txt"), []byte("before"))
}

func TestStoreSecondManagerUndoFromReloadsCompleteBatch(t *testing.T) {
	a, b := openParallelStoreManagers(t)
	first := recordParallelStoreFileChange(t, a, "batch-first", "batch", "app.txt", 1, []byte("base"), []byte("one"))
	second := recordParallelStoreFileChange(t, a, "batch-second", "batch", "app.txt", 2, []byte("one"), []byte("two"))
	ctx := context.Background()
	batch, err := b.UndoFrom(ctx, "batch", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Records) != 2 || batch.Records[0].ID != second.ID || batch.Records[1].ID != first.ID {
		t.Fatalf("second manager selected a stale or incorrectly ordered batch: %+v", batch)
	}
	assertFileBytes(t, filepath.Join(a.home, "app.txt"), []byte("base"))
	for _, record := range readParallelStoreRecords(t, a) {
		if !record.Undone {
			t.Fatalf("batch did not persist undo flag for %q", record.ID)
		}
	}
	if _, err := a.RedoIDs(ctx, "batch", []string{second.ID, first.ID}); err != nil {
		t.Fatalf("first manager could not redo newly undone batch: %v", err)
	}
	assertFileBytes(t, filepath.Join(a.home, "app.txt"), []byte("two"))
	for _, record := range readParallelStoreRecords(t, a) {
		if record.Undone {
			t.Fatalf("batch retained stale undo flag for %q", record.ID)
		}
	}
}

func TestStoreControllerUndoSelectsLatestFromSecondManager(t *testing.T) {
	a, b := openParallelStoreManagers(t)
	first := recordParallelStoreFileChange(t, a, "controller-first", "session", "first.txt", 1, []byte("first-before"), []byte("first-after"))
	controller := NewController(a, "session")
	second := recordParallelStoreFileChange(t, b, "controller-second", "session", "second.txt", 2, []byte("second-before"), []byte("second-after"))
	result, err := controller.Undo(context.Background())
	if err != nil || result.Record.ID != second.ID {
		t.Fatalf("Controller selected cached checkpoint instead of latest: record=%+v, error=%v", result.Record, err)
	}
	assertFileBytes(t, filepath.Join(a.home, "first.txt"), []byte("first-after"))
	assertFileBytes(t, filepath.Join(a.home, "second.txt"), []byte("second-before"))
	records := readParallelStoreRecords(t, a)
	if records[first.ID].Undone || !records[second.ID].Undone {
		t.Fatalf("Controller changed the wrong undo flag: %+v", records)
	}
	result, err = controller.Redo(context.Background())
	if err != nil || result.Record.ID != second.ID {
		t.Fatalf("Controller redo selected the wrong checkpoint: record=%+v, error=%v", result.Record, err)
	}
	assertFileBytes(t, filepath.Join(a.home, "first.txt"), []byte("first-after"))
	assertFileBytes(t, filepath.Join(a.home, "second.txt"), []byte("second-after"))
}
