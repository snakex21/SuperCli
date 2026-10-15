package processsession

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"supercli/internal/system/windowautomation"
)

// Done is evaluated when the cancellable acquisition enters its select, so
// tests wait for a real blocked waiter rather than sleeping or polling.
type gateWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *gateWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestWindowGateSerializesSameProcessAndReleasesWaiters(t *testing.T) {
	tool := New(t.TempDir())
	identity := windowautomation.Identity{PID: 1, Created: 10}
	first, err := tool.lockWindowProcess(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	waitCtx := &gateWaitContext{Context: ctx, entered: make(chan struct{})}
	acquired := make(chan func(), 1)
	failed := make(chan error, 1)
	go func() {
		unlock, err := tool.lockWindowProcess(waitCtx, identity)
		if err != nil {
			failed <- err
			return
		}
		acquired <- unlock
	}()
	<-waitCtx.entered
	select {
	case unlock := <-acquired:
		unlock()
		first()
		t.Fatal("concurrent operations entered the same process")
	default:
	}
	first()
	select {
	case unlock := <-acquired:
		unlock()
	case err := <-failed:
		t.Fatal(err)
	}
	if len(tool.windowGates) != 0 {
		t.Fatal("completed process operations retained idle gates")
	}
}

func TestWindowGateAllowsIndependentProcessesAndCancellation(t *testing.T) {
	tool := New(t.TempDir())
	a := windowautomation.Identity{PID: 1, Created: 10}
	first, err := tool.lockWindowProcess(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, identity := range []windowautomation.Identity{{PID: 2, Created: 20}, {PID: 1, Created: 11}} {
		unlock, err := tool.lockWindowProcess(ctx, identity)
		if err != nil {
			t.Fatalf("independent process was blocked: %v", err)
		}
		unlock()
	}
	waiting, stop := context.WithCancel(ctx)
	waitCtx := &gateWaitContext{Context: waiting, entered: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		unlock, err := tool.lockWindowProcess(waitCtx, a)
		if unlock != nil {
			unlock()
		}
		done <- err
	}()
	<-waitCtx.entered
	stop()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked acquisition ignored cancellation: %v", err)
	}
	if len(tool.windowGates) != 1 {
		t.Fatal("canceled waiter leaked a gate or discarded the active owner")
	}
}
