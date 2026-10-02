//go:build linux || darwin

package media

import (
	"context"
	"encoding/json"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReadImageRejectsNamedPipeBeforeOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipe.png")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Skip(err)
	}
	res, err := NewReadImage(dir, 0).Execute(context.Background(), json.RawMessage(`{"path":"pipe.png"}`))
	if err == nil || res.Err == nil {
		t.Fatal("accepted FIFO")
	}
}
