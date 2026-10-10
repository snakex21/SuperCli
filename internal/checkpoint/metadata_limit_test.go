package checkpoint

import (
	"bytes"
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

func TestMetadataLimitOversizedSparseFileRefusesBeforeOpenAndKeepsRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turns.json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	markMetadataSparseFixture(t, f)
	if _, err := f.WriteString(`[{"id":"legacy-protected"}]`); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(2 * retentionMaxMetadata); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	opens := 0
	data, err := readCheckpointMetadataWithOpen(path, func(string) (*os.File, error) {
		opens++
		return nil, errors.New("oversized metadata must not be opened")
	})
	if !errors.Is(err, ErrMetadataLimit) || data != nil || opens != 0 {
		t.Fatalf("oversized sparse file read: bytes=%d opens=%d err=%v", len(data), opens, err)
	}
	m := &Manager{meta: path, records: []Record{{ID: "already-loaded"}}, repoReady: true}
	if err := m.reloadRecordsLocked(); !errors.Is(err, ErrMetadataLimit) || len(m.records) != 1 || m.records[0].ID != "already-loaded" || !m.repoReady {
		t.Fatalf("oversized metadata silently became empty history: records=%+v ready=%v err=%v", m.records, m.repoReady, err)
	}
	after, err := os.Stat(path)
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || !os.SameFile(before, after) {
		t.Fatal("oversized legacy file changed:", err)
	}
}

func TestMetadataLimitChecksOpenedRegularFileBeforeReading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turns.json")
	if err := os.WriteFile(path, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := readCheckpointMetadataWithOpen(path, func(path string) (*os.File, error) {
		f, err := os.OpenFile(path, os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err := f.Truncate(retentionMaxMetadata + 1); err != nil {
			f.Close()
			return nil, err
		}
		return f, nil
	})
	if !errors.Is(err, ErrMetadataLimit) || data != nil {
		t.Fatalf("growth between Lstat and Open bypassed bound: bytes=%d err=%v", len(data), err)
	}
	opens := 0
	if _, err := readCheckpointMetadataWithOpen(filepath.Dir(path), func(string) (*os.File, error) {
		opens++
		return nil, nil
	}); !errors.Is(err, ErrStoreInventory) || opens != 0 {
		t.Fatalf("non-regular metadata was opened: opens=%d err=%v", opens, err)
	}
}

func TestMetadataLimitJSONStringCountMatchesActualEscapes(t *testing.T) {
	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}
	for _, input := range []string{
		"", "plain ASCII / slash", "<>&\"\\\b\f\n\r\t\x00\x01\x1f\x7f",
		"zażółć 🦉 \u2028\u2029 \ufffd", string(allBytes), string([]byte{0xff, 0xc0, 0xaf, 0xe2, 0x80}),
	} {
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		count, err := addCheckpointJSONStringBytes(0, input)
		if err != nil || count != int64(len(encoded)-2) {
			t.Fatalf("encoded string count differs: count=%d actual=%d err=%v", count, len(encoded)-2, err)
		}
	}
	records := []Record{{
		ID: "i<&", SessionID: "s>\"", Prompt: "prompt\u2028", Before: "before\\", After: "after\x00",
		Files: []string{"same&path", "other\tpath"}, Changes: []FileChange{{Path: "same&path", Kind: "modified\u2029"}},
	}}
	encoded, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	count, err := checkpointMetadataStringsLowerBound(records)
	if err != nil || count > int64(len(encoded)) {
		t.Fatalf("lower bound rejected an admissible encoding: count=%d actual=%d err=%v", count, len(encoded), err)
	}
	var measured int64
	var measureErr error
	if allocations := testing.AllocsPerRun(100, func() { measured, measureErr = checkpointMetadataStringsLowerBound(records) }); allocations != 0 || measured != count || measureErr != nil {
		t.Fatalf("string preflight allocates: allocs=%g count=%d err=%v", allocations, measured, measureErr)
	}
}

func TestMetadataLimitPreflightRefusesDuplicatedEscapedPathsWithoutMarshal(t *testing.T) {
	path := strings.Repeat("&", retentionMaxMetadata/12+1)
	r := Record{ID: "oversized", Files: []string{path}, Changes: []FileChange{{Path: path, Kind: "modified"}}}
	// Raw string contents fit the byte cap; the exact duplicate escaped content
	// exceeds it. This test intentionally never allocates its encoded JSON.
	if len(path)*2 >= retentionMaxMetadata {
		t.Fatal("fixture does not isolate encoded escape expansion")
	}
	if _, err := checkpointMetadataStringsLowerBound([]Record{r}); !errors.Is(err, ErrMetadataLimit) {
		t.Fatal("duplicated escaped paths reached MarshalIndent:", err)
	}
	if count, err := addCheckpointJSONStringBytes(retentionMaxMetadata, ""); err != nil || count != retentionMaxMetadata {
		t.Fatal("exact string lower-bound limit was falsely rejected:", count, err)
	}
	if _, err := addCheckpointJSONStringBytes(retentionMaxMetadata, "a"); !errors.Is(err, ErrMetadataLimit) {
		t.Fatal("lower-bound arithmetic passed the maximum:", err)
	}
}

func metadataBoundaryRecord(t *testing.T, target int) (Record, []byte) {
	t.Helper()
	path := "portable/" + strings.Repeat("&", 1<<18) + "/quote\"\\\u2028.txt"
	r := Record{ID: "boundary", SessionID: "synthetic", Prompt: "p", Files: []string{path}, Changes: []FileChange{{Path: path, Kind: "modified"}}}
	encoded, err := json.MarshalIndent([]Record{r}, "", "  ")
	if err != nil || len(encoded) >= target {
		t.Fatal("invalid boundary fixture:", len(encoded), err)
	}
	r.Prompt = strings.Repeat("p", target-len(encoded)+1)
	encoded, err = json.MarshalIndent([]Record{r}, "", "  ")
	if err != nil || len(encoded) != target {
		t.Fatal("boundary fixture does not match encoded bytes:", len(encoded), target, err)
	}
	return r, encoded
}

func TestMetadataLimitAdmissionUsesActualEncodedBytesAtBoundary(t *testing.T) {
	m := openSnapshotTestManager(t, t.TempDir())
	r, exact := metadataBoundaryRecord(t, retentionMaxMetadata)
	unlocked, err := m.lockStore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := unlocked(); err != nil {
			t.Error(err)
		}
	}()
	m.records = []Record{r}
	if err := m.saveLocked(); err != nil {
		t.Fatal("exact encoded maximum rejected:", err)
	}
	actual, err := readCheckpointMetadata(m.meta)
	if err != nil || !bytes.Equal(actual, exact) {
		t.Fatal("exact encoded bytes were not admitted/read:", err)
	}
	m.records[0].Prompt += "p"
	if count, err := checkpointMetadataStringsLowerBound(m.records); err != nil || count > retentionMaxMetadata {
		t.Fatal("fixture must pass lower bound and fail actual final size:", count, err)
	}
	if err := m.saveLocked(); !errors.Is(err, ErrMetadataLimit) {
		t.Fatal("one encoded byte over maximum was admitted:", err)
	}
	actual, err = readCheckpointMetadata(m.meta)
	if err != nil || !bytes.Equal(actual, exact) {
		t.Fatal("oversized admission changed original JSON:", err)
	}
	if _, err := os.Lstat(m.meta + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("oversized admission created staging:", err)
	}
}

func TestMetadataLimitFailedAppendPreservesOriginalJSONAndRefs(t *testing.T) {
	m := openSnapshotTestManager(t, t.TempDir())
	seed := recordParallelStoreFileChange(t, m, "retained", "synthetic", "source.bin", 1, []byte("before"), []byte("after"))
	metadata, err := os.ReadFile(m.meta)
	if err != nil {
		t.Fatal(err)
	}
	refs := recordRefsTestListing(t, m, "refs/supercli/records/")
	path := strings.Repeat("&", retentionMaxMetadata/12+1)
	r := Record{ID: "refused", SessionID: "synthetic", Before: seed.Before, After: seed.After, Files: []string{path}, Changes: []FileChange{{Path: path, Kind: "modified"}}}
	sentinel := []byte("existing staging must remain untouched before admission")
	if err := os.WriteFile(m.meta+".tmp", sentinel, 0600); err != nil {
		t.Fatal(err)
	}
	committed, err := m.appendCommitted(context.Background(), r)
	if committed || !errors.Is(err, ErrMetadataLimit) || len(m.records) != 1 || m.records[0].ID != seed.ID {
		t.Fatalf("oversized append changed in-memory history: committed=%v count=%d err=%v", committed, len(m.records), err)
	}
	assertFileBytes(t, m.meta, metadata)
	assertFileBytes(t, m.meta+".tmp", sentinel)
	if got := recordRefsTestListing(t, m, "refs/supercli/records/"); got != refs || recordRefsTestListing(t, m, recordRefRoot(r.ID)) != "" {
		t.Fatal("oversized append changed retained refs or left its new pair:", got)
	}
	assertFileBytes(t, filepath.Join(m.home, "source.bin"), []byte("after"))
}

func TestMetadataLimitFailedTurnKeepsBeforeAfterRecoveryPins(t *testing.T) {
	m := openSnapshotTestManager(t, t.TempDir())
	path := filepath.Join(m.home, "source.txt")
	before, after := []byte("before\r\n"), []byte("after\r\n")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	turn := m.NewTurn("synthetic", "synthetic oversized admission")
	// Deliberately inject pathological metadata below NewTurn's normal
	// 160-rune prompt cap; production prompts cannot create this fixture.
	turn.prompt = strings.Repeat("&", retentionMaxMetadata/6+1)
	t.Cleanup(func() {
		// Only this disposable synchronous fixture: shrink its prompt and drain
		// the existing retry owner before native lease/TempDir cleanup.
		turn.mu.Lock()
		turn.prompt = "bounded retry"
		turn.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := turn.Complete(ctx); err != nil {
			t.Error("complete synthetic metadata-limit fixture:", err)
			if turn.active != nil && turn.active.lease != nil && turn.active.lease.owner != nil {
				if err := turn.active.lease.owner.Close(); err != nil {
					t.Error("close synthetic lease:", err)
				}
			}
		}
	})
	write := tools.Tool{Name: "write_file", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Err: os.WriteFile(path, after, 0600)}, nil
	}}
	runSnapshotTool(t, turn.Wrap(write), `{"path":"source.txt"}`)
	if record, err := turn.Complete(context.Background()); record != nil || !errors.Is(err, ErrMetadataLimit) {
		t.Fatalf("oversized turn unexpectedly committed: hasRecord=%v err=%v", record != nil, err)
	}
	if turn.snapshotAfter == "" || pendingOwnerCount(t, m) != 1 || recordRefsTestListing(t, m, "refs/supercli/records/") != "" {
		t.Fatal("oversized turn lost its retry state or stranded an admitted record")
	}
	active := recordRefsTestListing(t, m, turn.active.root())
	if !strings.Contains(active, turn.active.root()+"/before "+turn.before) || !strings.Contains(active, turn.active.root()+"/after "+turn.snapshotAfter) {
		t.Fatal("oversized turn lost before/after recovery pins")
	}
	assertFileBytes(t, path, after)
	if _, err := m.git(context.Background(), "cat-file", "-e", turn.before); err != nil {
		t.Fatal("oversized turn lost its exact before snapshot:", err)
	}
}
