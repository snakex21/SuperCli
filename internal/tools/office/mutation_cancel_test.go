package office

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"supercli/internal/tools/fileops"
)

type queuedOfficeContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *queuedOfficeContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}
func TestOfficeEditsCancelWhileWaitingForFileOwner(t *testing.T) {
	for _, kind := range []string{"docx", "xlsx"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			name := "document." + kind
			if kind == "docx" {
				writeTestDocx(t, dir, name, []string{"original"})
			} else {
				writeTestXlsx(t, dir, name, twoByTwo())
			}
			full := filepath.Join(dir, name)
			before, err := os.ReadFile(full)
			if err != nil {
				t.Fatal(err)
			}
			release := fileops.LockMutationPaths(full)
			defer release()
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &queuedOfficeContext{Context: base, waiting: make(chan struct{})}
			done := make(chan error, 1)
			go func() {
				var err error
				if kind == "docx" {
					_, err = NewEditDocx(dir).Execute(ctx, json.RawMessage(`{"path":"document.docx","action":"replace","find":"original","replace":"late edit"}`))
				} else {
					_, err = NewEditXlsx(dir).Execute(ctx, json.RawMessage(`{"path":"document.xlsx","action":"set_cell","cell":"B2","value":99}`))
				}
				done <- err
			}()
			select {
			case <-ctx.waiting:
			case <-time.After(time.Second):
				t.Fatal("edit did not enter mutation queue")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled edit remained behind file owner")
			}
			after, err := os.ReadFile(full)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("canceled edit changed the document")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("canceled edit created backup/temporary files: %v", entries)
			}
		})
	}
}
