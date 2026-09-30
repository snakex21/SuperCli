package fileops

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMutationQueueSerializesSamePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "same.txt")
	release := LockMutationPaths(path)
	acquired := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		unlock := LockMutationPaths(path)
		close(acquired)
		unlock()
	}()

	select {
	case <-acquired:
		t.Fatal("same path was acquired while already locked")
	case <-time.After(30 * time.Millisecond):
	}
	release()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("same path did not unblock after release")
	}
	<-done
}

func TestMutationQueueDoesNotSerializeDifferentPaths(t *testing.T) {
	dir := t.TempDir()
	release := LockMutationPaths(filepath.Join(dir, "a.txt"))
	defer release()

	acquired := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		unlock := LockMutationPaths(filepath.Join(dir, "b.txt"))
		close(acquired)
		unlock()
	}()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("unrelated path was blocked")
	}
	<-done
}

func TestMutationQueueCanonicalizesAliasesAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	release := LockMutationPaths(path)
	acquired := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		unlock := LockMutationPaths(filepath.Join(dir, ".", "sub", "..", "file.txt"))
		close(acquired)
		unlock()
	}()

	select {
	case <-acquired:
		t.Fatal("canonical alias bypassed the path lock")
	case <-time.After(30 * time.Millisecond):
	}
	release()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("canonical alias did not unblock")
	}

	<-done
	if got := mutationQueueSize(); got != 0 {
		t.Fatalf("mutation queue retained %d entries", got)
	}
}

type observedMutationContext struct {
	context.Context
	attempts chan struct{}
}

func (c observedMutationContext) Done() <-chan struct{} {
	c.attempts <- struct{}{}
	return c.Context.Done()
}
func TestMutationQueueCancellationReleasesPartialAcquisition(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	release := LockMutationPaths(second)
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := make(chan struct{}, 3)
	done := make(chan error, 1)
	go func() {
		// Reverse input order and a duplicate test sorting and deduplication.
		unlock, err := LockMutationPathsContext(observedMutationContext{ctx, attempts}, second, first, first)
		if unlock != nil {
			unlock()
		}
		done <- err
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-attempts:
		case <-time.After(time.Second):
			t.Fatal("waiter did not reach both paths")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter remained behind owner")
	}
	if got := mutationQueueSize(); got != 1 {
		t.Fatalf("queue retained canceled references: %d entries", got)
	}
	fresh, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	unlock, err := LockMutationPathsContext(fresh, first)
	if err != nil {
		t.Fatalf("partial acquisition leaked: %v", err)
	}
	unlock()
}

type cancelOnMutationAcquire struct {
	context.Context
	cancel context.CancelFunc
	once   sync.Once
}

func (c *cancelOnMutationAcquire) Done() <-chan struct{} {
	c.once.Do(c.cancel)
	return c.Context.Done()
}
func TestMutationQueueRejectsCancellationWhenPermitIsAlsoReady(t *testing.T) {
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		unlock, err := LockMutationPathsContext(&cancelOnMutationAcquire{Context: ctx, cancel: cancel}, filepath.Join(t.TempDir(), "file"))
		cancel()
		if unlock != nil {
			unlock()
			t.Fatal("granted an already canceled acquisition")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	}
	if got := mutationQueueSize(); got != 0 {
		t.Fatalf("queue retained %d entries", got)
	}
}
