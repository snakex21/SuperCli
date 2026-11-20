package ctxexec

import (
	"context"
	"sync"
	"testing"
	"time"

	"supercli/internal/tools/core"
)

func TestLastOutputLine(t *testing.T) {
	for in, want := range map[string]string{
		"":                                  "",
		"a\nb\n\n":                          "b",
		"building\r[1/9]\r[2/9] engine\r\n": "[2/9] engine",
		"\x1b[32mok\x1b[0m  pkg\n":          "ok  pkg",
	} {
		if got := lastOutputLine(in); got != want {
			t.Errorf("lastOutputLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// A long command reports its newest output line as live tool progress (only
// when it changed) until it ends.
func TestCommandProgressReportsLatestLine(t *testing.T) {
	old := commandProgressEvery
	commandProgressEvery = 10 * time.Millisecond
	defer func() { commandProgressEvery = old }()
	var mu sync.Mutex
	var got []string
	ctx := core.WithToolProgress(context.Background(), func(p core.ToolProgress) {
		mu.Lock()
		got = append(got, p.Message)
		mu.Unlock()
	})
	act := &outputActivity{last: time.Now()}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() { reportCommandProgress(ctx, done, act); close(finished) }()
	act.note([]byte("compiling 1/2\n"))
	time.Sleep(60 * time.Millisecond)
	act.note([]byte("compiling 2/2\n"))
	time.Sleep(60 * time.Millisecond)
	close(done)
	<-finished
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != "compiling 1/2" || got[1] != "compiling 2/2" {
		t.Fatalf("progress = %q", got)
	}
}

// Without a live progress sink (no UI) the reporter stops at once.
func TestCommandProgressWithoutSinkStops(t *testing.T) {
	old := commandProgressEvery
	commandProgressEvery = 5 * time.Millisecond
	defer func() { commandProgressEvery = old }()
	act := &outputActivity{last: time.Now()}
	act.note([]byte("line\n"))
	finished := make(chan struct{})
	go func() { reportCommandProgress(context.Background(), make(chan struct{}), act); close(finished) }()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("reporter kept running without a progress sink")
	}
}
