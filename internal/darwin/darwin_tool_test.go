package darwin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func newTestTool(t *testing.T) *DarwinTool {
	t.Helper()
	return NewDarwinTool(providerStub{}, tools.NewRegistry(), t.TempDir(), "you are a stub")
}

func TestDarwinTool_SpecName(t *testing.T) {
	dt := newTestTool(t)
	spec := dt.Spec()
	if spec.Name != "darwin" {
		t.Errorf("Spec().Name = %q, want 'darwin'", spec.Name)
	}
}

func TestDarwinTool_SpecSchemaNonEmpty(t *testing.T) {
	dt := newTestTool(t)
	spec := dt.Spec()
	if spec.Schema == "" {
		t.Error("Spec().Schema is empty")
	}
	if !strings.Contains(spec.Schema, "prompt") {
		t.Error("Spec().Schema should mention 'prompt'")
	}
}

func TestDarwinTool_Run_EmptyPrompt(t *testing.T) {
	dt := newTestTool(t)
	dt.SetLoopFactory(func(LoopConfig) (Loop, error) {
		return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "x"}}}, nil
	})
	_, err := dt.run(context.Background(), json.RawMessage(`{"prompt":""}`))
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

func TestDarwinTool_Run_InvalidJSON(t *testing.T) {
	dt := newTestTool(t)
	dt.SetLoopFactory(func(LoopConfig) (Loop, error) {
		return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "x"}}}, nil
	})
	_, err := dt.run(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestDarwinTool_Run_UnknownJudge(t *testing.T) {
	dt := newTestTool(t)
	dt.SetLoopFactory(func(LoopConfig) (Loop, error) {
		return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "x"}}}, nil
	})
	_, err := dt.run(context.Background(), json.RawMessage(`{"prompt":"hi","judge":"oracle"}`))
	if err == nil {
		t.Fatal("expected error for unknown judge")
	}
	if !strings.Contains(err.Error(), "judge") {
		t.Errorf("error %q should mention 'judge'", err.Error())
	}
}

func TestDarwinTool_Run_PoolSizeTooLarge(t *testing.T) {
	dt := newTestTool(t)
	dt.SetLoopFactory(func(LoopConfig) (Loop, error) {
		return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "x"}}}, nil
	})
	_, err := dt.run(context.Background(), json.RawMessage(`{"prompt":"hi","pool_size":50}`))
	if err == nil {
		t.Fatal("expected error for pool_size > 10")
	}
}

func TestDarwinTool_Run_PoolSizeNegative(t *testing.T) {
	dt := newTestTool(t)
	dt.SetLoopFactory(func(LoopConfig) (Loop, error) {
		return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "x"}}}, nil
	})
	_, err := dt.run(context.Background(), json.RawMessage(`{"prompt":"hi","pool_size":-1}`))
	if err == nil {
		t.Fatal("expected error for pool_size < 0")
	}
}

func TestDarwinTool_Run_NoLoopFactory(t *testing.T) {
	dt := newTestTool(t)
	// Intentionally not calling SetLoopFactory.
	_, err := dt.run(context.Background(), json.RawMessage(`{"prompt":"hi"}`))
	if err == nil {
		t.Fatal("expected error when SetLoopFactory was not called")
	}
}

func TestDarwinTool_Spec_Fn_NilReceiver(t *testing.T) {
	var dt *DarwinTool
	spec := dt.Spec()
	if spec.Fn == nil {
		t.Fatal("Spec().Fn should not be nil even on nil receiver")
	}
	_, err := spec.Fn(context.Background(), json.RawMessage(`{"prompt":"hi"}`))
	if err == nil {
		t.Fatal("expected error when invoking Fn on nil DarwinTool")
	}
}

func TestRenderDarwinResult_AllFieldsPopulated(t *testing.T) {
	cands := []Candidate{
		{Index: 0, AgentID: "agent-1", Text: "first candidate answer"},
		{Index: 1, AgentID: "agent-2", Text: "second candidate answer"},
	}
	winner := cands[1]
	r := Result{
		Prompt:      "do the thing",
		Candidates:  cands,
		WinnerIndex: 1,
		Winner:      &winner,
		Score:       0.85,
		Reason:      "second was better",
		Merged:      true,
		MergeBranch: "darwin-x",
		TotalUsage:  llm.Usage{Input: 100, Output: 50, Total: 150},
	}
	out := renderDarwinResult(r)
	if !strings.Contains(out, "do the thing") {
		t.Error("missing prompt in output")
	}
	if !strings.Contains(out, "agent-2") {
		t.Error("missing winner agent id")
	}
	if !strings.Contains(out, "second was better") {
		t.Error("missing reason")
	}
	if !strings.Contains(out, "merged: yes") {
		t.Error("missing 'merged: yes' line")
	}
	if !strings.Contains(out, "darwin-x") {
		t.Error("missing merge branch")
	}
	if !strings.Contains(out, "150") {
		t.Error("missing total token count")
	}
	if !strings.Contains(out, "★") {
		t.Error("missing winner marker (★)")
	}
}

func TestRenderDarwinResult_MergeError(t *testing.T) {
	winner := Candidate{Index: 0, AgentID: "agent-1", Text: "x"}
	r := Result{
		Prompt:      "p",
		Candidates:  []Candidate{winner},
		WinnerIndex: 0,
		Winner:      &winner,
		Score:       0.5,
		Reason:      "ok",
		Merged:      false, // merge failed
	}
	out := renderDarwinResult(r)
	if !strings.Contains(out, "merged: no") {
		t.Error("expected 'merged: no' for a merge error / not-merged result")
	}
}

func TestRenderDarwinResult_NoWinner(t *testing.T) {
	r := Result{
		Prompt:      "p",
		Candidates:  []Candidate{{Index: 0, Err: errors.New("boom"), AgentID: "agent-1"}},
		WinnerIndex: -1,
	}
	out := renderDarwinResult(r)
	if !strings.Contains(out, "<none>") {
		t.Error("expected '<none>' for no winner")
	}
	if !strings.Contains(out, "FAILED") {
		t.Error("expected FAILED marker for failed candidate")
	}
}

func TestRenderDarwinResult_FailedCandidates(t *testing.T) {
	cands := []Candidate{
		{Index: 0, AgentID: "agent-1", Text: "good answer"},
		{Index: 1, AgentID: "agent-2", Err: errors.New("crashed")},
	}
	r := Result{
		Prompt:      "p",
		Candidates:  cands,
		WinnerIndex: 0,
		Winner:      &cands[0],
		Score:       0.6,
		Reason:      "ok",
	}
	out := renderDarwinResult(r)
	if !strings.Contains(out, "FAILED") {
		t.Error("expected FAILED marker for the failed candidate")
	}
	if !strings.Contains(out, "crashed") {
		t.Error("expected the error message in the FAILED line")
	}
	// The good answer should still appear as the winning answer.
	if !strings.Contains(out, "good answer") {
		t.Error("expected the winning answer text in the output")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 100); got != "short" {
		t.Errorf("truncate short = %q, want 'short'", got)
	}
	long := strings.Repeat("x", 200)
	got := truncate(long, 50)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncate long should end with '...', got %q", got)
	}
	if len(got) != 53 { // 50 chars + "..."
		t.Errorf("truncate long length = %d, want 53", len(got))
	}
}

func TestDarwinTool_RegisterInRealRegistry(t *testing.T) {
	dt := newTestTool(t)
	r := tools.NewRegistry()
	r.MustRegister(dt.Spec())
	got, ok := r.Get("darwin")
	if !ok {
		t.Fatal("darwin not registered in registry")
	}
	if got.Name != "darwin" {
		t.Errorf("registered name = %q, want 'darwin'", got.Name)
	}
	if got.Fn == nil {
		t.Error("registered Fn is nil")
	}
	if err := got.Validate(); err != nil {
		t.Errorf("registered tool failed validation: %v", err)
	}
}

func TestDarwinTool_RegisterDuplicateFails(t *testing.T) {
	dt := newTestTool(t)
	r := tools.NewRegistry()
	r.MustRegister(dt.Spec())
	// Registering the same name twice should fail.
	if err := r.Register(dt.Spec()); err == nil {
		t.Fatal("expected error on duplicate registration")
	}
}
