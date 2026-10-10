package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestPrepareArgsSharesExecutionRulesWithoutExecution(t *testing.T) {
	calls := 0
	r := NewRegistry()
	r.MustRegister(Tool{Name: "fixture", Description: "fixture", Schema: `{"type":"object","properties":{"count":{"type":"integer","minimum":1}},"required":["count"],"additionalProperties":false}`,
		RepairArgs: func(raw json.RawMessage) (json.RawMessage, bool) {
			if string(raw) == `{"legacy":2}` {
				return json.RawMessage(`{"count":"2"}`), true
			}
			return nil, false
		},
		Fn: func(_ context.Context, args json.RawMessage) (Result, error) {
			calls++
			return Result{Text: string(args)}, nil
		}})
	for _, raw := range []string{`{"count":1}`, `{"count":"1"}`, `{"legacy":2}`} {
		prepared, err := r.PrepareArgs("fixture", json.RawMessage(raw))
		if err != nil || calls != 0 {
			t.Fatalf("prepare executed or failed: %s %v", raw, err)
		}
		res, err := r.Execute(context.Background(), "fixture", json.RawMessage(raw))
		if err != nil || res.Err != nil || res.Text != string(prepared) {
			t.Fatalf("execution differs: prepared=%s res=%+v err=%v", prepared, res, err)
		}
		calls = 0
	}
	for _, raw := range []string{`{"count":0}`, `{"extra":1}`, `{`} {
		_, err := r.PrepareArgs("fixture", json.RawMessage(raw))
		if !errors.Is(err, ErrInvalidToolArgs) || calls != 0 {
			t.Fatalf("invalid accepted/executed: %s %v", raw, err)
		}
	}
	if _, err := r.PrepareArgs("missing", json.RawMessage(`{}`)); !errors.Is(err, ErrUnknownTool) {
		t.Fatal("unknown tool accepted")
	}
}
