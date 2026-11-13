package webgui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestToolDataTrackerPublishesAfterResult(t *testing.T) {
	var tr toolDataTracker
	if k := tr.observe(wireEvent{Type: "tool_call", Name: "goal", ID: "a"}); k != "" {
		t.Fatalf("tool_call must not publish before the write finished: %q", k)
	}
	tr.observe(wireEvent{Type: "tool_call", Name: "read_lines", ID: "b"})
	tr.observe(wireEvent{Type: "tool_call", Name: "remember", ID: "c"})
	if k := tr.observe(wireEvent{Type: "tool_result", ID: "b"}); k != "" {
		t.Fatalf("unrelated tool published %q", k)
	}
	if k := tr.observe(wireEvent{Type: "tool_result", ID: "a"}); k != "goal" {
		t.Fatalf("goal result kind = %q", k)
	}
	if k := tr.observe(wireEvent{Type: "tool_result", ID: "a"}); k != "" {
		t.Fatalf("duplicate result published %q", k)
	}
	if k := tr.observe(wireEvent{Type: "tool_result", ID: "c"}); k != "memory" {
		t.Fatalf("remember result kind = %q", k)
	}
}

func nextDataFrame(t *testing.T, sub *syncSubscriber) syncFrame {
	t.Helper()
	for {
		select {
		case f := <-sub.events:
			if f.Type == "data_changed" {
				return f
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no data_changed frame")
		}
	}
}

func TestDataChangedReachesEverySubscriber(t *testing.T) {
	h := &syncHub{}
	a, _, closeA := h.subscribe("s1", "a")
	defer closeA()
	b, _, closeB := h.subscribe("", "b")
	defer closeB()
	h.dataChanged("goal")
	for _, sub := range []*syncSubscriber{a, b} {
		if f := nextDataFrame(t, sub); f.Kind != "goal" {
			t.Fatalf("frame = %+v", f)
		}
	}
}

func TestGoalAndMemoryMutationsPublishDataChanged(t *testing.T) {
	srv := newTestServer(t, false)
	sub, _, done := srv.eng.syncHub.subscribe("", "observer")
	defer done()

	rec := httptest.NewRecorder()
	srv.handleGoal(rec, httptest.NewRequest(http.MethodPost, "/api/goal", strings.NewReader(`{"action":"set","title":"Live goal"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("goal set: %d %s", rec.Code, rec.Body.String())
	}
	if f := nextDataFrame(t, sub); f.Kind != "goal" {
		t.Fatalf("goal frame = %+v", f)
	}

	// A rejected mutation changed nothing and must stay quiet.
	rec = httptest.NewRecorder()
	srv.handleGoal(rec, httptest.NewRequest(http.MethodPost, "/api/goal", strings.NewReader(`{"action":"set","title":""}`)))
	if rec.Code == http.StatusOK {
		t.Fatal("empty title accepted")
	}
	select {
	case f := <-sub.events:
		if f.Type == "data_changed" {
			t.Fatalf("failed mutation published %+v", f)
		}
	default:
	}

	rec = httptest.NewRecorder()
	srv.handleMemory(rec, httptest.NewRequest(http.MethodPost, "/api/memory", strings.NewReader(`{"content":"live memory fact"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("memory add: %d %s", rec.Code, rec.Body.String())
	}
	if f := nextDataFrame(t, sub); f.Kind != "memory" {
		t.Fatalf("memory frame = %+v", f)
	}
}
