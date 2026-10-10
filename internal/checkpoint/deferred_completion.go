package checkpoint

import (
	"context"
	"time"
)

// CompleteDeferred preserves the inline path for normal turns. Only a turn
// with accepted work still running needs one event-driven background finalizer.
// The original caller's deadline/cancellation is not retained by that waiter.
// done must be a long-lived sink, never a closed response's HTTP/SSE writer.
func (t *Turn) CompleteDeferred(ctx context.Context, done func(*Record, error)) (*Record, error) {
	ready, immediate := t.Seal()
	if immediate {
		return t.Complete(ctx)
	}
	// An accepted worker may hold Turn.mu while capturing. Its foreground
	// completion and key must not wait for that mutex or its filesystem work.
	t.deferredRequested.Store(true)
	_, keyErr := t.ensureCompletionIdentity()
	if keyErr == nil && t.mu.TryLock() {
		keyErr = t.ensureActivePinsLocked()
		t.mu.Unlock()
	}
	t.deferredOnce.Do(func() {
		go func() {
			<-ready
			finishCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			record, err := t.Complete(finishCtx)
			if done != nil {
				done(record, err)
			}
		}()
	})
	return nil, keyErr
}

// CompletionKey is nonempty only for deferred completion. It survives release
// of active pins and is known before the foreground summary is inserted.
func (t *Turn) CompletionKey() string {
	if t.deferredRequested.Load() {
		if identity := t.completionIdentity.Load(); identity != nil {
			return *identity
		}
	}
	return ""
}

func (t *Turn) ensureCompletionIdentity() (string, error) {
	if identity := t.completionIdentity.Load(); identity != nil {
		return *identity, nil
	}
	id, err := newActivePinID()
	if err != nil {
		return "", err
	}
	if t.completionIdentity.CompareAndSwap(nil, &id) {
		return id, nil
	}
	return *t.completionIdentity.Load(), nil
}

func (c *Controller) takeOldestTurn() *Turn {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.turns) == 0 {
		return nil
	}
	turn := c.turns[0]
	c.turns[0] = nil // A sliced queue must not retain completed Turn/Manager.
	c.turns = c.turns[1:]
	return turn
}

func (c *Controller) CompleteDeferred(ctx context.Context, done func(*Record, error)) (*Record, error) {
	turn := c.takeOldestTurn()
	if turn == nil {
		return nil, nil
	}
	return turn.CompleteDeferred(ctx, done)
}
