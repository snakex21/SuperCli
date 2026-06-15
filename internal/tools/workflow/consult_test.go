package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/llm/consult"
)

// stub for tool tests: reuses the same shape as
// the consult package tests but lives here so
// tools/ doesn't depend on the test exports of
// consult/.
func toolStubProvider(name, text string, out int) *toolP {
	return &toolP{name: name, text: text, out: out}
}

type toolP struct {
	name string
	text string
	out  int
}

func (s *toolP) Name() string { return s.name }
func (s *toolP) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta, 2)
	ch <- llm.Delta{Content: s.text}
	ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 10, Output: s.out, Total: 10 + s.out}}
	close(ch)
	return ch, nil
}

type toolJudge struct {
	body string
}

func (j *toolJudge) Name() string { return "judge" }
func (j *toolJudge) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta, 2)
	ch <- llm.Delta{Content: j.body}
	ch <- llm.Delta{FinishReason: "stop"}
	close(ch)
	return ch, nil
}

func TestConsultTool_BasicInvoke(t *testing.T) {
	council := &consult.Council{
		Samples: []llm.Provider{
			toolStubProvider("a", "answer A", 20),
			toolStubProvider("b", "answer B", 25),
			toolStubProvider("c", "answer C", 15),
		},
		Judge: &toolJudge{body: `{"winner": 2, "reason": "B is the most complete"}`},
	}
	c := NewConsult(council)
	res, err := c.Spec().Fn(context.Background(), json.RawMessage(`{"question": "what is 2+2?"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Err != nil {
		t.Errorf("unexpected err: %v", res.Err)
	}
	if !strings.Contains(res.Text, "answer B") {
		t.Errorf("text should contain winner's response, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "B is the most complete") {
		t.Errorf("text should contain judge reason, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "[consult:") {
		t.Errorf("text should contain token marker, got %q", res.Text)
	}
}

func TestConsultTool_NilCouncil(t *testing.T) {
	c := &Consult{Council: nil}
	res, _ := c.Spec().Fn(context.Background(), json.RawMessage(`{"question": "q"}`))
	if !strings.Contains(res.Text, "not wired") {
		t.Errorf("nil council should return friendly text, got %+v", res)
	}
}

func TestConsultTool_EmptyQuestion(t *testing.T) {
	council := &consult.Council{
		Samples: []llm.Provider{toolStubProvider("a", "x", 1)},
		Judge:   &toolJudge{body: `{"winner": 1, "reason": "x"}`},
	}
	c := NewConsult(council)
	res, _ := c.Spec().Fn(context.Background(), json.RawMessage(`{"question": "  "}`))
	if res.Err == nil {
		t.Errorf("empty question should error, got text %q", res.Text)
	}
}

func TestConsultTool_BadJSON(t *testing.T) {
	council := &consult.Council{
		Samples: []llm.Provider{toolStubProvider("a", "x", 1)},
		Judge:   &toolJudge{body: `{"winner": 1, "reason": "x"}`},
	}
	c := NewConsult(council)
	res, _ := c.Spec().Fn(context.Background(), json.RawMessage(`not json`))
	if res.Err == nil {
		t.Errorf("bad JSON should error, got text %q", res.Text)
	}
}

func TestConsultTool_NClamping(t *testing.T) {
	// n=10 with only 3 samples → clamped to 3.
	council := &consult.Council{
		Samples: []llm.Provider{
			toolStubProvider("a", "x", 1),
			toolStubProvider("b", "y", 1),
			toolStubProvider("c", "z", 1),
		},
		Judge: &toolJudge{body: `{"winner": 1, "reason": "x"}`},
	}
	c := NewConsult(council)
	c.MaxN = 3
	res, _ := c.Spec().Fn(context.Background(), json.RawMessage(`{"question": "q", "n": 10}`))
	if res.Err != nil {
		t.Errorf("n=10 clamped should NOT error: %v", res.Err)
	}
}

func TestConsultTool_OnResultFires(t *testing.T) {
	council := &consult.Council{
		Samples: []llm.Provider{
			toolStubProvider("a", "x", 1),
			toolStubProvider("b", "y", 1),
		},
		Judge: &toolJudge{body: `{"winner": 1, "reason": "x"}`},
	}
	calls := int32(0)
	c := NewConsult(council)
	c.OnResult = func(r consult.Result) {
		atomic.AddInt32(&calls, 1)
		if r.Verdict.WinnerIndex != 0 {
			t.Errorf("OnResult WinnerIndex = %d, want 0", r.Verdict.WinnerIndex)
		}
		if len(r.Candidates) != 2 {
			t.Errorf("OnResult cands = %d, want 2", len(r.Candidates))
		}
	}
	_, _ = c.Spec().Fn(context.Background(), json.RawMessage(`{"question": "q"}`))
	if calls != 1 {
		t.Errorf("OnResult called %d times, want 1", calls)
	}
}

func TestConsultTool_AllFailedReturnsFriendlyText(t *testing.T) {
	failing := &failProvider{}
	council := &consult.Council{
		Samples: []llm.Provider{failing, failing},
		Judge:   &toolJudge{body: `{"winner": 1, "reason": "x"}`},
	}
	c := NewConsult(council)
	res, _ := c.Spec().Fn(context.Background(), json.RawMessage(`{"question": "q"}`))
	if res.Err != nil {
		t.Errorf("AllFailed should NOT be an error (model can recover), got %v", res.Err)
	}
	if !strings.Contains(res.Text, "all sample providers failed") {
		t.Errorf("text should be friendly 'all sample providers failed', got %q", res.Text)
	}
}

type failProvider struct{}

type errStr string

func (e errStr) Error() string { return string(e) }

func (f *failProvider) Name() string { return "fail" }
func (f *failProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta, 1)
	ch <- llm.Delta{Err: errStr("provider down")}
	close(ch)
	return ch, nil
}
