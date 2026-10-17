// Package maintenance coordinates cancellable, idle-only housekeeping with chats.
package maintenance

import (
	"context"
	"sync"
)

var activity struct {
	sync.Mutex
	runs   int
	cancel context.CancelFunc
	done   chan struct{}
}

// BeginRun gives foreground work priority. Cancellation interrupts SQLite and
// checkpoint enumeration before the chat proceeds; no new maintenance can enter.
func BeginRun() func() {
	activity.Lock()
	activity.runs++
	done := activity.done
	if activity.cancel != nil {
		activity.cancel()
	}
	activity.Unlock()
	if done != nil {
		<-done
	}
	var once sync.Once
	return func() { once.Do(func() { activity.Lock(); activity.runs--; activity.Unlock() }) }
}

// WhileIdle never waits for a chat or another housekeeping task.
func WhileIdle(parent context.Context, fn func(context.Context)) bool {
	activity.Lock()
	if activity.runs != 0 || activity.done != nil {
		activity.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	activity.cancel, activity.done = cancel, done
	activity.Unlock()
	defer func() {
		cancel()
		activity.Lock()
		activity.cancel, activity.done = nil, nil
		close(done)
		activity.Unlock()
	}()
	fn(ctx)
	return true
}
