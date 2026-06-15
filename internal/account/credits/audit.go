package credits

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// AuditEvent records a single tool call. It is written
// to <dataDir>/logs/audit.log as one JSON object per
// line. The schema is deliberately small so the file
// stays small in long sessions.
type AuditEvent struct {
	TS       int64  `json:"ts"`               // unix nano
	Tool     string `json:"tool"`             // tool name
	Op       string `json:"op,omitempty"`     // op type (read/write/delete/bash/network)
	Path     string `json:"path,omitempty"`   // first path-like arg if any
	Args     string `json:"args,omitempty"`   // JSON string of all args (or key args)
	Result   string `json:"result,omitempty"` // "ok" / "err" / short summary
	Error    string `json:"error,omitempty"`  // err.Error() if any
	Duration int64  `json:"duration_ms,omitempty"`
	Session  string `json:"session,omitempty"`
}

// Audit is a non-blocking, in-process JSONL audit log
// writer. The caller fires-and-forgets Audit events;
// a background goroutine drains the channel and writes
// to disk. This is intentionally simple — we do NOT
// try to make this durable across crashes for F7.
type Audit struct {
	dir string

	// in is buffered to absorb bursts; full channel
	// drops the event with a counter.
	in        chan AuditEvent
	dropped   atomic.Uint64
	processed atomic.Uint64

	stopCh chan struct{}
	doneCh chan struct{}
	wg     sync.WaitGroup

	// mu protects fd. The writer goroutine holds fd
	// the whole time; mu is only relevant on Close.
	mu sync.Mutex
	fd *os.File
}

// NewAudit creates an audit log rooted at <home>/logs/.
// The channel capacity is 256 by default; an F8+ build
// may let the user configure it.
func NewAudit(home string) (*Audit, error) {
	if home == "" {
		return nil, fmt.Errorf("credits: NewAudit: empty home")
	}
	dir := filepath.Join(home, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("credits: NewAudit: mkdir %q: %w", dir, err)
	}
	a := &Audit{
		dir:    dir,
		in:     make(chan AuditEvent, 4096),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	a.wg.Add(1)
	go a.loop()
	return a, nil
}

// Log enqueues an event. Never blocks; never panics.
// Returns true if the event was queued, false if it
// was dropped because the channel was full.
func (a *Audit) Log(ev AuditEvent) bool {
	if a == nil {
		return false
	}
	if ev.TS == 0 {
		ev.TS = time.Now().UnixNano()
	}
	select {
	case a.in <- ev:
		return true
	default:
		a.dropped.Add(1)
		return false
	}
}

// LogSync is a small helper that fills the obvious
// fields and enqueues. Use this from tool wrappers:
//
//	audit.LogSync(audit.AuditEvent{Tool: "file_write", Op: "write", Path: p, Result: "ok"})
func (a *Audit) LogSync(ev AuditEvent) bool {
	return a.Log(ev)
}

// Dropped returns the number of events that could not be
// enqueued because the channel was full. The TUI may
// show this as a warning.
func (a *Audit) Dropped() uint64 {
	if a == nil {
		return 0
	}
	return a.dropped.Load()
}

// Pending returns the number of events currently in the
// channel waiting to be written. The test helper and
// the TUI use this to wait for the drain to finish.
func (a *Audit) Pending() int {
	if a == nil {
		return 0
	}
	return len(a.in)
}

// Processed returns the number of events that have
// been written to disk.
func (a *Audit) Processed() uint64 {
	if a == nil {
		return 0
	}
	return a.processed.Load()
}

// Close stops the background writer and flushes any
// pending events. Safe to call multiple times.
func (a *Audit) Close() error {
	if a == nil {
		return nil
	}
	select {
	case <-a.stopCh:
		return nil // already closed
	default:
	}
	close(a.stopCh)
	<-a.doneCh
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fd != nil {
		err := a.fd.Close()
		a.fd = nil
		return err
	}
	return nil
}

// Path returns the absolute path to the audit log file.
// The directory containing this file is exposed via
// Dir() for the "tail" / "rotate" helpers.
func (a *Audit) Path() string {
	return filepath.Join(a.dir, "audit.log")
}

func (a *Audit) Dir() string { return a.dir }

// loop is the background drain.
func (a *Audit) loop() {
	defer a.wg.Done()
	defer close(a.doneCh)

	// Open with O_APPEND so multiple processes (rare
	// but possible) don't trample each other; O_CREATE
	// is implied.
	f, err := os.OpenFile(a.Path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		// We can't log anywhere useful. Drain the
		// channel silently and exit.
		for {
			select {
			case _, ok := <-a.in:
				if !ok {
					return
				}
			case <-a.stopCh:
				return
			}
		}
	}
	a.mu.Lock()
	a.fd = f
	a.mu.Unlock()
	w := bufio.NewWriter(f)
	defer w.Flush()

	flush := func() {
		if err := w.Flush(); err != nil {
			// Best effort; the next write will
			// surface the error again.
			_ = err
		}
	}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	for {
		select {
		case ev, ok := <-a.in:
			if !ok {
				flush()
				return
			}
			if err := writeEvent(w, ev); err != nil {
				a.dropped.Add(1)
			} else {
				a.processed.Add(1)
			}
		case <-tick.C:
			flush()
		case <-a.stopCh:
			// Drain events that are already buffered, but do not wait
			// for new ones. Waiting here makes interactive quit feel
			// sluggish even when there is nothing left to write.
			for {
				select {
				case ev, ok := <-a.in:
					if !ok {
						flush()
						return
					}
					_ = writeEvent(w, ev)
				default:
					flush()
					return
				}
			}
		}
	}

}

func writeEvent(w *bufio.Writer, ev AuditEvent) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// Tail reads the last n lines of the audit log. If the
// log does not exist, returns nil and no error. Used by
// the --status flag.
func Tail(home string, n int) ([]AuditEvent, error) {
	if home == "" {
		return nil, fmt.Errorf("credits: Tail: empty home")
	}
	if n <= 0 {
		return []AuditEvent{}, nil
	}
	path := filepath.Join(home, "logs", "audit.log")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("credits: Tail: open: %w", err)
	}
	defer f.Close()

	// Two-pass: count lines, then seek to the right
	// offset. Cheap enough for an audit log that
	// should stay under a few MB.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n {
			lines = lines[len(lines)-n:]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("credits: Tail: scan: %w", err)
	}
	out := make([]AuditEvent, 0, len(lines))
	for _, line := range lines {
		var ev AuditEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			// Skip malformed lines silently —
			// partial writes happen during crash.
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}
