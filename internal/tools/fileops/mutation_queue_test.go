package fileops

import (
	"path/filepath"
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
	go func() {
		unlock := LockMutationPaths(filepath.Join(dir, "b.txt"))
		close(acquired)
		unlock()
	}()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("unrelated path was blocked")
	}
}

func TestMutationQueueCanonicalizesAliasesAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	release := LockMutationPaths(path)
	acquired := make(chan struct{})
	go func() {
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

	deadline := time.Now().Add(time.Second)
	for mutationQueueSize() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := mutationQueueSize(); got != 0 {
		t.Fatalf("mutation queue retained %d entries", got)
	}
}
