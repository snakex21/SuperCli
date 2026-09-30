package files

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"supercli/internal/tools/fileops"
)

func mutationFixtureSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			out[rel] = "directory"
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = string(raw)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

type mutationCancelCase struct {
	name   string
	spec   func(string) Tool
	args   string
	locked string
}

func mutationCancelCases() []mutationCancelCase {
	return []mutationCancelCase{
		{"patch", func(d string) Tool { return NewPatchFile(d).Spec() }, `{"path":"source.txt","old":"original","new":"late edit"}`, "source.txt"},
		{"write", func(d string) Tool { return NewWriteFile(d).Spec() }, `{"path":"source.txt","content":"late write"}`, "source.txt"},
		{"create", func(d string) Tool { return NewCreateFile(d).Spec() }, `{"path":"nested/new.txt","content":"late create"}`, "nested/new.txt"},
		{"mkdir", func(d string) Tool { return NewMakeDir(d).Spec() }, `{"path":"nested/new"}`, "nested/new"},
		{"move", func(d string) Tool { return NewMove(d).Spec() }, `{"src":"source.txt","dest":"moved/source.txt"}`, "source.txt"},
		{"copy", func(d string) Tool { return NewCopy(d).Spec() }, `{"src":"source.txt","dest":"copied/source.txt"}`, "source.txt"},
		{"trash", func(d string) Tool { return NewTrash(d).Spec() }, `{"path":"source.txt"}`, "source.txt"},
	}
}
func TestFileMutationsHonorCancellation(t *testing.T) {
	for _, tc := range mutationCancelCases() {
		for _, queued := range []bool{false, true} {
			label := "already_canceled"
			if queued {
				label = "waiting_for_owner"
			}
			t.Run(tc.name+"/"+label, func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
				before := mutationFixtureSnapshot(t, dir)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				release := func() {}
				if queued {
					release = fileops.LockMutationPaths(filepath.Join(dir, tc.locked))
				}
				defer func() { release() }()
				done := make(chan error, 1)
				started := make(chan struct{})
				if !queued {
					cancel()
				}
				go func() {
					close(started)
					result, err := tc.spec(dir).Fn(ctx, json.RawMessage(tc.args))
					if err == nil {
						err = result.Err
					}
					done <- err
				}()
				<-started
				cancel()
				var err error
				select {
				case err = <-done:
				case <-time.After(200 * time.Millisecond):
					t.Error("canceled mutation waited for the other owner")
					release()
					release = func() {}
					select {
					case err = <-done:
					case <-time.After(2 * time.Second):
						t.Fatal("mutation did not finish after owner release")
					}
				}
				if !errors.Is(err, context.Canceled) {
					t.Errorf("error=%v, want context.Canceled", err)
				}
				after := mutationFixtureSnapshot(t, dir)
				if !reflect.DeepEqual(before, after) {
					t.Errorf("canceled mutation changed files: before=%v after=%v", before, after)
				}
			})
		}
	}
}
