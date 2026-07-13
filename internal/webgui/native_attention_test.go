package webgui

import (
	"sync/atomic"
	"testing"
)

func TestNativeCompletionHook(t *testing.T) {
	var calls atomic.Int32
	unregister := registerNativeCompletion(func() { calls.Add(1) })
	signalNativeRunCompleted()
	signalNativeRunCompleted()
	if got := calls.Load(); got != 2 {
		t.Fatalf("completion calls = %d, want 2", got)
	}
	unregister()
	signalNativeRunCompleted()
	if got := calls.Load(); got != 2 {
		t.Fatalf("unregistered hook was called: %d", got)
	}
}
