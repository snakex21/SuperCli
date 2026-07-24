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
type lineWriter struct {
	mu sync.Mutex
	f  *os.File
	sc *bufio.Writer
}

func newLineWriter(path string) (*lineWriter, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	return &lineWriter{f: f, sc: bufio.NewWriter(f)}, nil
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.sc.Write(p)
	if err != nil {
		return n, err
	}
	if err := w.sc.Flush(); err != nil {
		return n, err
	}
	return n, nil
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
