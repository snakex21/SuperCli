package core

import (
	"bufio"
	"fmt"
	"os"
	"sync"
)

// lineWriter is a thin file wrapper that flushes on every
// Write so a crash does not lose log lines. Used by
// ErrorLog. The mutex allows concurrent Append calls.
//
// When max is positive the file is size-capped: once it grows past
// the cap the current file is rotated to "<path>.1" (replacing any
// previous rotation) and a fresh file is started. Two files is the
// whole retention policy — an append-only diagnostic log must never
// grow without bound on a machine nobody prunes.
type lineWriter struct {
	mu   sync.Mutex
	path string
	max  int64
	size int64
	f    *os.File
	sc   *bufio.Writer
}

// newLineWriter opens path for appending. maxBytes <= 0 disables
// rotation.
func newLineWriter(path string, maxBytes int64) (*lineWriter, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	w := &lineWriter{path: path, max: maxBytes, f: f, sc: bufio.NewWriter(f)}
	if fi, err := f.Stat(); err == nil {
		w.size = fi.Size()
	}
	return w, nil
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sc == nil {
		// A failed rotation left us without a file. Drop the line
		// rather than break the agent loop.
		return len(p), nil
	}
	n, err := w.sc.Write(p)
	if err != nil {
		return n, err
	}
	if err := w.sc.Flush(); err != nil {
		return n, err
	}
	w.size += int64(n)
	if w.max > 0 && w.size >= w.max {
		w.rotate()
	}
	return n, nil
}

// rotate closes the current file, moves it aside and opens a fresh
// one. The caller must hold w.mu. Every failure mode is non-fatal:
// the worst outcome is that logging stops, never that the caller
// does.
func (w *lineWriter) rotate() {
	if w.sc != nil {
		_ = w.sc.Flush()
	}
	if w.f != nil {
		_ = w.f.Close()
	}
	w.f, w.sc = nil, nil
	// Rename may fail (another process holds the file open on
	// Windows, read-only directory). In that case we simply reopen
	// the original and re-read its size, so we do not attempt a
	// rotation on every subsequent write.
	_ = os.Remove(w.path + ".1")
	_ = os.Rename(w.path, w.path+".1")
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	w.f, w.sc, w.size = f, bufio.NewWriter(f), 0
	if fi, err := f.Stat(); err == nil {
		w.size = fi.Size()
	}
}

func (w *lineWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sc != nil {
		_ = w.sc.Flush()
	}
	if w.f != nil {
		return w.f.Close()
	}
	return nil
}
