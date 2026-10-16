package webgui

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSyncTimestampsLiveAndReplay(t *testing.T) {
	h := &syncHub{}
	sub, empty, unsubscribe := h.subscribe("s", "phone")
	defer unsubscribe()
	before := time.Now().UnixMilli()
	if empty.ServerNowMS <= 0 {
		t.Fatal("empty snapshot has no server clock")
	}
	run := h.start("s", "hello", syncIdentity{"turn", "pc"}, func() {})
	started := <-sub.events
	<-sub.events // sessions_changed
	if started.StartedAtMS < before || started.ServerNowMS < started.StartedAtMS {
		t.Fatalf("invalid run start clocks: %+v", started)
	}
	types := []string{"tool_call", "worker_progress", "tool_result", "worker", "done"}
	for _, typ := range types {
		h.event(run, wireEvent{Type: typ})
		frame := <-sub.events
		if frame.Event == nil || frame.Event.At < started.StartedAtMS || frame.ServerNowMS < frame.Event.At {
			t.Fatalf("missing/invalid event clock: %+v", frame)
		}
	}
	// Explicit emission timestamps must survive buffering, not be replaced at join.
	at := time.Now().Add(-time.Minute).UnixMilli()
	h.event(run, wireEvent{Type: "tool_call", ID: "older", At: at})
	live := <-sub.events
	_, snapshot, closeReplay := h.subscribe("s", "late-phone")
	defer closeReplay()
	if len(snapshot.Runs) != 1 || snapshot.Runs[0].StartedAtMS != started.StartedAtMS || snapshot.ServerNowMS < live.ServerNowMS {
		t.Fatalf("snapshot lost run clock: %+v", snapshot)
	}
	if len(snapshot.Events) != len(types)+1 {
		t.Fatalf("events: %+v", snapshot.Events)
	}
	for _, ev := range snapshot.Events {
		if ev.At <= 0 {
			t.Fatalf("unstamped replay: %+v", ev)
		}
	}
	if got := snapshot.Events[len(types)].At; got != at || live.Event.At != at {
		t.Fatalf("replay reset clock: %d != %d", got, at)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var encoded struct {
		Now  int64 `json:"server_now_ms"`
		Runs []struct {
			Start int64 `json:"started_at_ms"`
		} `json:"runs"`
		Events []struct {
			At int64 `json:"at"`
		} `json:"events"`
	}
	if err := json.Unmarshal(data, &encoded); err != nil {
		t.Fatal(err)
	}
	if encoded.Now == 0 || encoded.Runs[0].Start == 0 || encoded.Events[len(types)].At != at {
		t.Fatalf("wire clocks missing: %s", data)
	}
}
