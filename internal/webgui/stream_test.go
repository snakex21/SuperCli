package webgui

import (
	"errors"
	"strings"
	"testing"

	"supercli/internal/agent"
)

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
