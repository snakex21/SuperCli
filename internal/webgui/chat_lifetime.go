package webgui

import (
	"context"
	"sync"
)

// Chat generation belongs to the engine, not to a browser connection.
type chatLifetime struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
	wg     sync.WaitGroup
}

func (e *Engine) beginChatRun(base context.Context) (context.Context, context.CancelFunc, func()) {
	l := &e.chatLifetime
	l.mu.Lock()
	if l.ctx == nil {
		l.ctx, l.cancel = context.WithCancel(context.Background())
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(base))
	if l.closed {
		cancel()
		l.mu.Unlock()
		return ctx, cancel, func() {}
	}
	stop := context.AfterFunc(l.ctx, cancel)
	l.wg.Add(1)
	l.mu.Unlock()
	return ctx, cancel, func() { stop(); cancel(); l.wg.Done() }
}

func (e *Engine) stopChatRuns() {
	l := &e.chatLifetime
	l.mu.Lock()
	l.closed = true
	if l.cancel != nil {
		l.cancel()
	}
	l.mu.Unlock()
	e.syncHub.close()
}
