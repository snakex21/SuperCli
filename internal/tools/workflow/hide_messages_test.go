package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// stubHider records HideRange calls and supports LenFn.
type stubHider struct {
	hidden []int // [from, to) ranges pushed by callers
}

func (s *stubHider) HideRange(from, to int) error {
	s.hidden = append(s.hidden, from, to)
	return nil
}

func (s *stubHider) Len() int { return 10 } // arbitrary, used for range validation

func TestHideMessagesTool_BasicHide(t *testing.T) {
	s := &stubHider{}
	tool := NewHideMessages(s, s.Len)
	res, _ := tool.Spec().Fn(context.Background(), json.RawMessage(`{"from": 2, "to": 5}`))
	if res.Err != nil {
		t.Fatalf("Err: %v", res.Err)
	}
	if len(s.hidden) != 2 || s.hidden[0] != 2 || s.hidden[1] != 5 {
		t.Errorf("hidden = %v, want [2, 5]", s.hidden)
	}
	if !strings.Contains(res.Text, "hid 3") {
		t.Errorf("text should say 'hid 3', got %q", res.Text)
	}
}

func TestHideMessagesTool_NilHider(t *testing.T) {
	tool := &HideMessages{Hider: nil, LenFn: nil}
	res, _ := tool.Spec().Fn(context.Background(), json.RawMessage(`{"from": 0, "to": 1}`))
	if res.Err == nil && !strings.Contains(res.Text, "not wired") {
		t.Errorf("expected Err or 'not wired' text, got %+v", res)
	}
}

func TestHideMessagesTool_BadJSON(t *testing.T) {
	s := &stubHider{}
	tool := NewHideMessages(s, s.Len)
	res, _ := tool.Spec().Fn(context.Background(), json.RawMessage(`not json`))
	if res.Err == nil {
		t.Errorf("expected Err for bad JSON, got text: %s", res.Text)
	}
}

func TestHideMessagesTool_NegativeIndex(t *testing.T) {
	s := &stubHider{}
	tool := NewHideMessages(s, s.Len)
	for _, body := range []string{
		`{"from": -1, "to": 5}`,
		`{"from": 0, "to": -1}`,
		`{"from": 5, "to": 2}`,
	} {
		res, _ := tool.Spec().Fn(context.Background(), json.RawMessage(body))
		if res.Err == nil {
			t.Errorf("%s: expected error, got text %q", body, res.Text)
		}
	}
}

func TestHideMessagesTool_EmptyRange(t *testing.T) {
	s := &stubHider{}
	tool := NewHideMessages(s, s.Len)
	res, _ := tool.Spec().Fn(context.Background(), json.RawMessage(`{"from": 2, "to": 2}`))
	if res.Err != nil {
		t.Errorf("empty range should be no-op, got Err: %v", res.Err)
	}
	if !strings.Contains(res.Text, "empty range") {
		t.Errorf("text should say 'empty range', got %q", res.Text)
	}
	if len(s.hidden) != 0 {
		t.Errorf("Hider.HideRange should NOT be called for empty range, got %v", s.hidden)
	}
}

func TestHideMessagesTool_OutOfRange(t *testing.T) {
	s := &stubHider{}
	tool := NewHideMessages(s, s.Len) // LenFn returns 10
	res, _ := tool.Spec().Fn(context.Background(), json.RawMessage(`{"from": 0, "to": 100}`))
	if res.Err == nil {
		t.Errorf("expected error for out-of-range, got text: %s", res.Text)
	}
}

// _ uses llm package to ensure imports stay.
var _ = llm.RoleUser
