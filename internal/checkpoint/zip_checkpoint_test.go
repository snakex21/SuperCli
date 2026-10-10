package checkpoint

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"supercli/internal/tools"
)

func checkpointZIPFixture(t *testing.T, home string) {
	t.Helper()
	f, err := os.Create(filepath.Join(home, "assets.zip"))
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for _, name := range []string{"existing.txt", "new.txt"} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("extracted-" + name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestZIPReadsSkipCheckpointAndExtractCapturesOnlyDestination(t *testing.T) {
	for _, controller := range []bool{false, true} {
		t.Run(map[bool]string{false: "turn", true: "controller"}[controller], func(t *testing.T) {
			ctx := context.Background()
			home := t.TempDir()
			checkpointZIPFixture(t, home)
			writeCheckpointFixture(t, filepath.Join(home, "dest/existing.txt"), "original")
			writeOversizedSnapshotFixture(t, home)
			m := openSnapshotTestManager(t, home)
			turn := m.NewTurn("zip", "extract assets")
			var complete func(context.Context) (*Record, error) = turn.Complete
			spec := tools.NewReadZip(home, 0).Spec()
			if controller {
				c := NewController(m, "zip")
				c.Start("extract assets")
				spec = c.Wrap(spec)
				complete = c.Complete
			} else {
				spec = turn.Wrap(spec)
			}
			runSnapshotTool(t, spec, `{"path":"assets.zip","action":"list"}`)
			runSnapshotTool(t, spec, `{"path":"assets.zip","action":"read","pattern":"existing.txt"}`)
			if m.repoReady {
				t.Fatal("read-only ZIP initialized checkpoint repo")
			}
			runSnapshotTool(t, spec, `{"path":"assets.zip","action":"extract","target_dir":"dest"}`)
			record, err := complete(ctx)
			if err != nil || record == nil {
				t.Fatalf("record=%v err=%v", record, err)
			}
			if !reflect.DeepEqual(record.Files, []string{"dest/existing.txt", "dest/new.txt"}) {
				t.Fatalf("unrelated inputs captured: %v", record.Files)
			}
			if _, err := m.Undo(ctx, record.ID); err != nil {
				t.Fatal(err)
			}
			assertFileBytes(t, filepath.Join(home, "dest/existing.txt"), []byte("original"))
			if _, err := os.Stat(filepath.Join(home, "dest/new.txt")); !os.IsNotExist(err) {
				t.Fatal("created entry remained after undo")
			}
			if _, err := m.Redo(ctx, record.ID); err != nil {
				t.Fatal(err)
			}
			assertFileBytes(t, filepath.Join(home, "dest/existing.txt"), []byte("extracted-existing.txt"))
			assertFileBytes(t, filepath.Join(home, "dest/new.txt"), []byte("extracted-new.txt"))
		})
	}
}

func TestZIPCustomDefaultExtractionRetainsCheckpoint(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	checkpointZIPFixture(t, home)
	tool := tools.NewReadZip(home, 0)
	tool.ExtractRoot = filepath.Join(home, "custom-root")
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("zip-custom", "extract")
	result, err := turn.Wrap(tool.Spec()).Fn(ctx, json.RawMessage(`{"path":"assets.zip","action":"extract"}`))
	if err != nil || result.Err != nil {
		t.Fatalf("extract: %v %v", err, result.Err)
	}
	record, err := turn.Complete(ctx)
	if err != nil || record == nil || len(record.Files) != 2 {
		t.Fatalf("default-root record=%+v err=%v", record, err)
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range record.Files {
		if _, err := os.Stat(filepath.Join(home, path)); !os.IsNotExist(err) {
			t.Fatalf("entry remained: %s", path)
		}
	}
	if _, err := m.Redo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
}
