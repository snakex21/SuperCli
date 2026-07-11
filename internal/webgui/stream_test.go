package webgui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
)

type stalledWebProvider struct{}

func (stalledWebProvider) Name() string { return "stalled" }

func (stalledWebProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

func TestToWireEvent_Message(t *testing.T) {
	w, keep := toWireEvent(agent.MessageEvent{Text: "hello"})
	if !keep {
		t.Fatal("message event should be kept")
	}
	if w.Type != "message" || w.Text != "hello" {
		t.Errorf("got %+v", w)
	}
}

func TestToWireEvent_ToolCall(t *testing.T) {
	w, keep := toWireEvent(agent.ToolCallEvent{Name: "write_file", Args: `{"path":"x"}`, ID: "t1"})
	if !keep {
		t.Fatal("tool_call should be kept")
	}
	if w.Type != "tool_call" || w.Name != "write_file" || w.ID != "t1" {
		t.Errorf("got %+v", w)
	}
}

func TestToWireEvent_ToolResultWithError(t *testing.T) {
	w, _ := toWireEvent(agent.ToolResultEvent{ID: "t1", Err: errors.New("boom")})
	if w.Type != "tool_result" || w.Err != "boom" {
		t.Errorf("got %+v", w)
	}
}

func TestToWireEvent_ToolResultSuccess(t *testing.T) {
	w, _ := toWireEvent(agent.ToolResultEvent{ID: "t1", Output: "ok"})
	if w.Err != "" || w.Output != "ok" {
		t.Errorf("expected no error, got %+v", w)
	}
}

func TestToWireEvent_Done(t *testing.T) {
	w, keep := toWireEvent(agent.DoneEvent{Usage: agent.Usage{Input: 10, Output: 5, Total: 15}})
	if !keep || w.Type != "done" {
		t.Fatalf("got %+v keep=%v", w, keep)
	}
	if w.TokIn != 10 || w.TokOut != 5 || w.TokTotal != 15 {
		t.Errorf("usage mismatch: %+v", w)
	}
	// No cache/reasoning reported → both omitted.
	if w.CacheHitPct != 0 || w.ReasoningTok != 0 {
		t.Errorf("expected zero observability, got %+v", w)
	}
}

func TestToWireEvent_DoneObservability(t *testing.T) {
	w, _ := toWireEvent(agent.DoneEvent{Usage: agent.Usage{Input: 2000, Output: 100, Total: 2100, Cached: 1500, Reasoning: 80}})
	if w.CacheHitPct != 75 {
		t.Errorf("cache-hit%% = %d, want 75", w.CacheHitPct)
	}
	if w.TokCached != 1500 {
		t.Errorf("tok_cached = %d, want 1500", w.TokCached)
	}
	if w.ReasoningTok != 80 {
		t.Errorf("reasoning = %d, want 80", w.ReasoningTok)
	}
}

func TestToWireEvent_Worker(t *testing.T) {
	w, keep := toWireEvent(agent.WorkerNotificationEvent{TaskID: "t1", Agent: "general", Status: "done", Summary: "did it", Text: "full report"})
	if !keep || w.Type != "worker" {
		t.Fatalf("got %+v keep=%v", w, keep)
	}
	if w.ID != "t1" || w.Name != "general" || w.Status != "done" || w.Output != "did it" || w.Text != "full report" {
		t.Errorf("worker fields: %+v", w)
	}
}

func TestToWireEvent_DraftUsed(t *testing.T) {
	w, keep := toWireEvent(agent.DraftUsedEvent{Decision: "accepted", DraftModel: "small", VerifierModel: "big", Savings: 42})
	if !keep || w.Type != "notice" {
		t.Fatalf("got %+v keep=%v", w, keep)
	}
	for _, want := range []string{"accepted", "small", "big", "42"} {
		if !strings.Contains(w.Text, want) {
			t.Errorf("draft notice missing %q: %q", want, w.Text)
		}
	}
}

func TestToWireEvent_MessagesHidden(t *testing.T) {
	w, keep := toWireEvent(agent.MessagesHiddenEvent{Count: 3, Reason: "budget"})
	if !keep || w.Type != "notice" || !strings.Contains(w.Text, "3") || !strings.Contains(w.Text, "budget") {
		t.Errorf("got %+v keep=%v", w, keep)
	}
}

func TestToWireEvent_Error(t *testing.T) {
	w, _ := toWireEvent(agent.ErrorEvent{Err: errors.New("failed")})
	if w.Type != "error" || w.Err != "failed" {
		t.Errorf("got %+v", w)
	}
}

func TestToWireEvent_ErrorNil(t *testing.T) {
	// Defensive: an ErrorEvent with a nil Err must not panic.
	w, _ := toWireEvent(agent.ErrorEvent{})
	if w.Type != "error" || w.Err != "" {
		t.Errorf("got %+v", w)
	}
}

func TestToWireEvent_Reflection(t *testing.T) {
	w, keep := toWireEvent(agent.ReflectionEvent{Step: 3, Text: "thinking"})
	if !keep || w.Type != "reflection" || w.Step != 3 {
		t.Errorf("got %+v keep=%v", w, keep)
	}
}

func TestToWireEvent_Compact(t *testing.T) {
	w, keep := toWireEvent(agent.AutoCompactEvent{Reason: "auto", Removed: 4, Window: 16384})
	if !keep || w.Type != "compact" {
		t.Fatalf("got %+v", w)
	}
	if !strings.Contains(w.Text, "auto") || !strings.Contains(w.Text, "4") {
		t.Errorf("compact text missing detail: %q", w.Text)
	}
}

func TestWireEvent_Marshal(t *testing.T) {
	w := wireEvent{Type: "message", Text: "hi"}
	b := w.marshal()
	s := string(b)
	if !strings.Contains(s, `"type":"message"`) || !strings.Contains(s, `"text":"hi"`) {
		t.Errorf("marshal output: %s", s)
	}
	// Omitempty: a bare message must not carry tool/usage fields.
	if strings.Contains(s, "tok_total") || strings.Contains(s, `"name"`) {
		t.Errorf("expected omitted empty fields, got %s", s)
	}
}

func TestRunStreamStopsAfterSemanticProgressTimeout(t *testing.T) {
	srv := newTestServer(t, false)
	id := seedWebSession(t, srv, srv.eng.Home(), "Existing")
	srv.eng.mu.Lock()
	srv.eng.prov = stalledWebProvider{}
	srv.eng.cfg.Timeout = 40 * time.Millisecond
	srv.eng.mu.Unlock()

	started := time.Now()
	err := srv.eng.runStream(context.Background(), "hello", id, func(wireEvent) {})
	if err == nil || !strings.Contains(err.Error(), "no model progress") {
		t.Fatalf("runStream error = %v, want semantic progress timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("progress timeout took too long: %s", elapsed)
	}
}
