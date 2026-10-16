package webgui

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestSyncHubReplayIsolationAndCleanup(t *testing.T) {
	h := &syncHub{}
	owner, _, closeOwner := h.subscribe("s", "a")
	defer closeOwner()
	other, _, closeOther := h.subscribe("elsewhere", "b")
	defer closeOther()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := h.start("s", "hello", syncIdentity{"turn", "a"}, cancel)
	for _, sub := range []*syncSubscriber{owner, other} {
		<-sub.events
		<-sub.events
	}
	for i := 0; i < 1000; i++ {
		h.event(run, wireEvent{Type: "message", Text: "x"})
	}
	h.event(run, wireEvent{Type: "reasoning", Text: "why", ReasoningTok: 2})
	h.event(run, wireEvent{Type: "reasoning", Text: "?", ReasoningTok: 3})
	h.event(run, wireEvent{Type: "question", Question: &questionWire{ID: "q"}})
	h.event(run, wireEvent{Type: "question_closed", ID: "q"})
	_, snapshot, unsubscribe := h.subscribe("s", "b")
	defer unsubscribe()
	if len(snapshot.Runs) != 1 || len(snapshot.Events) != 3 || len(snapshot.Events[0].Text) != 1000 || snapshot.Events[1].Text != "why?" || snapshot.Events[1].ReasoningTok != 5 {
		t.Fatalf("snapshot: %+v", snapshot)
	}
	select {
	case frame := <-owner.events:
		t.Fatalf("owner duplicate: %+v", frame)
	default:
	}
	select {
	case frame := <-other.events:
		t.Fatalf("unselected event: %+v", frame)
	default:
	}
	if h.stop("s", "stale") || !h.stop("s", "turn") || ctx.Err() == nil {
		t.Fatal("turn-specific Stop failed")
	}
	h.event(run, wireEvent{Type: "done"})
	h.finish(run, nil)
	_, snapshot, end := h.subscribe("s", "b")
	defer end()
	if len(snapshot.Runs) != 0 || len(snapshot.Events) != 0 || run.events != nil || h.stop("s", "turn") {
		t.Fatal("finished replay retained")
	}
}

func TestSyncHubSlowSubscriberAndConcurrentReplay(t *testing.T) {
	h := &syncHub{}
	run := h.start("s", "hello", syncIdentity{}, func() {})
	slow, _, unsub := h.subscribe("s", "b")
	defer unsub()
	for i := 0; i < 129; i++ {
		h.event(run, wireEvent{Type: "message", Text: "x"})
	}
	count := 0
	for range slow.events {
		count++
	}
	if count != 128 {
		t.Fatal(count)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				_, snap, done := h.subscribe("s", fmt.Sprint(i))
				if len(snap.Events) == 0 {
					t.Error("missing replay")
				}
				done()
				h.event(run, wireEvent{Type: "message", Text: "y"})
			}
		}(i)
	}
	wg.Wait()
	h.finish(run, context.Canceled)
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.runs) != 0 || len(h.subscribers) != 0 {
		t.Fatal("hub leaked state")
	}
}
