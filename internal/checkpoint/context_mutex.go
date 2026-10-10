package checkpoint

import (
	"context"
	"sync"
)

// contextMutex keeps ordinary lock ownership while allowing invocation admission
// to abandon a contended wait. It has no waiter goroutine or polling loop.
type contextMutex struct {
	once sync.Once
	busy chan struct{}
}

func (m *contextMutex) init() {
	m.once.Do(func() { m.busy = make(chan struct{}, 1) })
}

func (m *contextMutex) Lock() {
	m.init()
	m.busy <- struct{}{}
}

func (m *contextMutex) LockContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.init()
	select {
	case m.busy <- struct{}{}:
		if err := ctx.Err(); err != nil {
			m.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *contextMutex) TryLock() bool {
	m.init()
	select {
	case m.busy <- struct{}{}:
		return true
	default:
		return false
	}
}

func (m *contextMutex) Unlock() {
	m.init()
	select {
	case <-m.busy:
	default:
		panic("checkpoint: unlock of unlocked mutex")
	}
}
