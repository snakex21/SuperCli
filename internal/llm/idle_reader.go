package llm

import (
	"io"
	"time"
)

// idleTimeoutReader wraps a streaming response body and aborts the request
// (via cancel) if no data arrives within idle. The timer resets on every
// successful read, so a slow-but-alive model is tolerated while a truly
// stalled connection is cut. idle<=0 disables the watchdog.
type idleTimeoutReader struct {
	rc     io.ReadCloser
	idle   time.Duration
	cancel func()
	timer  *time.Timer
}

func newIdleTimeoutReader(rc io.ReadCloser, idle time.Duration, cancel func()) *idleTimeoutReader {
	r := &idleTimeoutReader{rc: rc, idle: idle, cancel: cancel}
	if idle > 0 {
		r.timer = time.AfterFunc(idle, cancel)
	}
	return r
}

func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	n, err := r.rc.Read(p)
	if n > 0 && r.timer != nil {
		r.timer.Reset(r.idle)
	}
	return n, err
}

func (r *idleTimeoutReader) Close() error {
	if r.timer != nil {
		r.timer.Stop()
	}
	return r.rc.Close()
}
