package webgui

import (
	"context"
	"errors"
	"sync"
	"time"
)

// errSessionBusy is a sentinel; the browser renders it from the session.busy
// catalog key.
var errSessionBusy = errors.New("conversation is already running")

// sessionRunWait covers the short tail of a stopped run (draining and saving)
// for clients that start the next turn without waiting for a receipt.
const sessionRunWait = 3 * time.Second

// claimSessionRun admits one run per conversation. With wait > 0 it waits that
// long for the previous run to release the conversation.
func (e *Engine) claimSessionRun(ctx context.Context, id string, wait time.Duration) (func(), error) {
	var timeout <-chan time.Time
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		timeout = timer.C
	}
	for {
		e.sessionRunMu.Lock()
		busy, running := e.sessionRuns[id]
		if !running {
			if e.sessionRuns == nil {
				e.sessionRuns = make(map[string]chan struct{})
			}
			done := make(chan struct{})
			e.sessionRuns[id] = done
			e.sessionRunMu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					e.sessionRunMu.Lock()
					delete(e.sessionRuns, id)
					e.sessionRunMu.Unlock()
					close(done)
				})
			}, nil
		}
		e.sessionRunMu.Unlock()
		if timeout == nil {
			return nil, errSessionBusy
		}
		select {
		case <-busy:
		case <-timeout:
			return nil, errSessionBusy
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (e *Engine) sessionRunActive(id string) bool {
	e.sessionRunMu.Lock()
	defer e.sessionRunMu.Unlock()
	_, running := e.sessionRuns[id]
	return running
}
