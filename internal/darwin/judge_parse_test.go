package darwin

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestParseJudgeVerdict_ValidJSON(t *testing.T) {
	v, err := parseJudgeVerdict(`{"winner": 1, "score": 0.8, "reason": "best"}`, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.WinnerIndex != 0 {
		t.Errorf("winner = %d, want 0 (1-based 1)", v.WinnerIndex)
	}
	if v.Score != 0.8 {
		t.Errorf("score = %f, want 0.8", v.Score)
	}
	if v.Reason != "best" {
		t.Errorf("reason = %q, want best", v.Reason)
	}
}

func TestParseJudgeVerdict_CodeFenceJSON(t *testing.T) {
	s := "Here is the verdict:\n```json\n{\"winner\": 2, \"score\": 0.7, \"reason\": \"good\"}\n```\nDone."
	v, err := parseJudgeVerdict(s, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.WinnerIndex != 1 {
		t.Errorf("winner = %d, want 1 (1-based 2)", v.WinnerIndex)
	}
	if v.Score != 0.7 {
		t.Errorf("score = %f, want 0.7", v.Score)
	}
	if v.Reason != "good" {
		t.Errorf("reason = %q, want good", v.Reason)
	}
}

func TestParseJudgeVerdict_CodeFenceNoLang(t *testing.T) {
	s := "```\n{\"winner\": 1, \"score\": 0.5, \"reason\": \"ok\"}\n```"
	v, err := parseJudgeVerdict(s, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.WinnerIndex != 0 {
		t.Errorf("winner = %d, want 0", v.WinnerIndex)
	}
	if v.Score != 0.5 {
		t.Errorf("score = %f, want 0.5", v.Score)
	}
}

func TestParseJudgeVerdict_ProseAroundJSON(t *testing.T) {
	s := "I think the answer is {\"winner\": 1, \"score\": 0.6, \"reason\": \"chosen\"} hopefully."
	v, err := parseJudgeVerdict(s, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.WinnerIndex != 0 {
		t.Errorf("winner = %d, want 0", v.WinnerIndex)
	}
	if v.Score != 0.6 {
		t.Errorf("score = %f, want 0.6", v.Score)
	}
	if v.Reason != "chosen" {
		t.Errorf("reason = %q, want chosen", v.Reason)
	}
}

func TestParseJudgeVerdict_InvalidJSON(t *testing.T) {
	_, err := parseJudgeVerdict("not json at all just prose", 3)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestParseJudgeVerdict_EmptyString(t *testing.T) {
	_, err := parseJudgeVerdict("", 3)
	if err == nil {
		t.Fatal("expected error for empty string, got nil")
	}
}

func TestParseJudgeVerdict_WinnerZero(t *testing.T) {
	_, err := parseJudgeVerdict(`{"winner": 0, "score": 0.5, "reason": "x"}`, 3)
	if err == nil {
		t.Fatal("expected error for winner=0, got nil")
	}
}

func TestParseJudgeVerdict_NegativeWinner(t *testing.T) {
	_, err := parseJudgeVerdict(`{"winner": -1, "score": 0.5, "reason": "x"}`, 3)
	if err == nil {
		t.Fatal("expected error for negative winner, got nil")
	}
}

func TestParseJudgeVerdict_WinnerOutOfRange(t *testing.T) {
	_, err := parseJudgeVerdict(`{"winner": 100, "score": 0.5, "reason": "x"}`, 3)
	if err == nil {
		t.Fatal("expected error for winner out of range, got nil")
	}
}

func TestParseJudgeVerdict_ScoreGreaterThanOneClamped(t *testing.T) {
	v, err := parseJudgeVerdict(`{"winner": 1, "score": 1.5, "reason": "x"}`, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Score != 1.0 {
		t.Errorf("score = %f, want 1.0", v.Score)
	}
}

func TestParseJudgeVerdict_ScoreLessThanZeroClamped(t *testing.T) {
	v, err := parseJudgeVerdict(`{"winner": 1, "score": -0.5, "reason": "x"}`, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Score != 0.0 {
		t.Errorf("score = %f, want 0.0", v.Score)
	}
}

func TestParseJudgeVerdict_MissingScore(t *testing.T) {
	// Missing score field: JSON unmarshalling leaves it at 0.0
	// (the zero value for float64). The source only clamps when
	// Score < 0, so a missing field stays at 0.
	v, err := parseJudgeVerdict(`{"winner": 1, "reason": "x"}`, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Score != 0.0 {
		t.Errorf("score = %f, want 0.0 (missing field default)", v.Score)
	}
}

func TestRenderJudgePrompt_ContainsAllCandidates(t *testing.T) {
	cands := []Candidate{
		{Index: 0, Text: "first candidate text", AgentID: "agent-1"},
		{Index: 1, Text: "second candidate text", AgentID: "agent-2"},
		{Index: 2, Err: errStub("failed"), Text: "third (failed) text", AgentID: "agent-3"},
	}
	prompt := renderJudgePrompt("the original task", cands)
	if !strings.Contains(prompt, "the original task") {
		t.Error("prompt does not include the task description")
	}
	if !strings.Contains(prompt, "first candidate text") {
		t.Error("prompt does not include first candidate's text")
	}
	if !strings.Contains(prompt, "second candidate text") {
		t.Error("prompt does not include second candidate's text")
	}
	if !strings.Contains(prompt, "third (failed) text") {
		t.Error("prompt does not include third candidate's text")
	}
	if !strings.Contains(prompt, "candidate 1") {
		t.Error("prompt does not include 'candidate 1' label")
	}
	if !strings.Contains(prompt, "candidate 2") {
		t.Error("prompt does not include 'candidate 2' label")
	}
	if !strings.Contains(prompt, "candidate 3") {
		t.Error("prompt does not include 'candidate 3' label")
	}
	if !strings.Contains(prompt, "FAILED") {
		t.Error("prompt does not include FAILED marker for the errored candidate")
	}
}

func TestRenderJudgePrompt_TruncatesLongText(t *testing.T) {
	long := strings.Repeat("x", 5000)
	cands := []Candidate{{Index: 0, Text: long, AgentID: "agent-1"}}
	prompt := renderJudgePrompt("task", cands)
	if strings.Contains(prompt, long) {
		t.Errorf("prompt should not contain the full 5000-char text (got length %d)", strings.Count(prompt, "x"))
	}
	if !strings.Contains(prompt, "truncated") {
		t.Error("prompt should contain the truncation marker")
	}
	// The truncation should leave the first 4000 chars.
	prefix := strings.Repeat("x", 4000)
	if !strings.Contains(prompt, prefix) {
		t.Error("prompt should contain the first 4000 chars of the long text")
	}
}

// errStub is a tiny test-only error type.
type errStub string

func (e errStub) Error() string { return string(e) }

// stubProvider is a minimal llm.Provider for LLMJudge tests.
type stubProvider struct {
	name   string
	deltas []llm.Delta
	err    error
	// msgs receives the messages sent to Complete, for assertions.
	msgs chan<- []llm.Message
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	if s.msgs != nil {
		select {
		case s.msgs <- msgs:
		default:
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	ch := make(chan llm.Delta, len(s.deltas)+1)
	for _, d := range s.deltas {
		ch <- d
	}
	close(ch)
	return ch, nil
}

func TestLLMJudge_NilProvider(t *testing.T) {
	var j *LLMJudge
	_, err := j.Judge(context.Background(), "test", []Candidate{{Index: 0, Text: "x"}})
	if err == nil {
		t.Fatal("expected error for nil LLMJudge, got nil")
	}
	j = &LLMJudge{Provider: nil}
	_, err = j.Judge(context.Background(), "test", []Candidate{{Index: 0, Text: "x"}})
	if err == nil {
		t.Fatal("expected error for nil provider, got nil")
	}
}

func TestLLMJudge_NoCandidates(t *testing.T) {
	p := &stubProvider{name: "stub"}
	j := &LLMJudge{Provider: p}
	_, err := j.Judge(context.Background(), "test", nil)
	if err == nil {
		t.Fatal("expected error for no candidates, got nil")
	}
}

func TestLLMJudge_ValidResponse(t *testing.T) {
	p := &stubProvider{
		name: "stub",
		deltas: []llm.Delta{
			{Content: `{"winner": 1, "score": 0.7, "reason": "good"}`},
			{FinishReason: "stop"},
		},
	}
	j := &LLMJudge{Provider: p}
	v, err := j.Judge(context.Background(), "test", []Candidate{
		{Index: 0, Text: "first"},
		{Index: 1, Text: "second"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.WinnerIndex != 0 {
		t.Errorf("winner = %d, want 0 (1-based 1)", v.WinnerIndex)
	}
	if v.Score != 0.7 {
		t.Errorf("score = %f, want 0.7", v.Score)
	}
	if v.Reason != "good" {
		t.Errorf("reason = %q, want good", v.Reason)
	}
}

func TestLLMJudge_ProviderError(t *testing.T) {
	p := &stubProvider{name: "stub", err: errStub("network down")}
	j := &LLMJudge{Provider: p}
	_, err := j.Judge(context.Background(), "test", []Candidate{{Index: 0, Text: "x"}})
	if err == nil {
		t.Fatal("expected error from failing provider, got nil")
	}
}

func TestLLMJudge_InvalidResponseJSON(t *testing.T) {
	p := &stubProvider{
		name: "stub",
		deltas: []llm.Delta{
			{Content: "totally not json"},
			{FinishReason: "stop"},
		},
	}
	j := &LLMJudge{Provider: p}
	_, err := j.Judge(context.Background(), "test", []Candidate{{Index: 0, Text: "x"}})
	if err == nil {
		t.Fatal("expected error from invalid response, got nil")
	}
}
