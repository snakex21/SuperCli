package maintenance

import (
	"context"
	"testing"
	"time"
)

func TestRunningChatSkipsMaintenance(t *testing.T) {
	end, err := BeginPortableRun(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	if WhileIdle(context.Background(), func(context.Context) { t.Fatal("maintenance ran during chat") }) {
		t.Fatal("reported idle")
	}
}

func TestChatCancelsMaintenance(t *testing.T) {
	entered := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		WhileIdle(context.Background(), func(ctx context.Context) { close(entered); <-ctx.Done() })
	}()
	<-entered
	end := BeginRun()
	defer end()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("chat did not interrupt maintenance")
	}
}

func TestPortableSharedLeaseSkipsMaintenance(t *testing.T) {
	root := t.TempDir()
	file, err := openActivityFile(root)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	unlock, err := lockActivityFile(file, false)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	WhilePortableIdle(context.Background(), root, func(context.Context) { t.Fatal("exclusive maintenance entered shared chat lease") })
}

func TestPortableRemoteCancellationRequest(t *testing.T) {
	root := t.TempDir()
	entered := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		WhilePortableIdle(context.Background(), root, func(ctx context.Context) { close(entered); <-ctx.Done() })
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("maintenance did not enter")
	}
	remote, err := openActivityFile(root)
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	if _, err := remote.WriteAt([]byte{1}, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("remote cancellation byte was ignored")
	}
	unlock, err := lockActivityFile(remote, false)
	if err != nil {
		t.Fatal("cancelled maintenance kept exclusive lease:", err)
	}
	unlock()
}

func TestPortableSharedLeasesAllowConcurrentChats(t *testing.T) {
	root := t.TempDir()
	first, err := BeginPortableRun(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	second, err := BeginPortableRun(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer second()
}

func TestPortableExclusiveLeaseCancelledForChat(t *testing.T) {
	root := t.TempDir()
	entered := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		WhilePortableIdle(context.Background(), root, func(ctx context.Context) { close(entered); <-ctx.Done() })
	}()
	<-entered
	end, err := BeginPortableRun(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("portable maintenance not cancelled")
	}
}
