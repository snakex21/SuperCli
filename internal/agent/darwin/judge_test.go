package darwin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestHeuristicJudge_EmptyCandidates(t *testing.T) {
	j := NewHeuristicJudge()
	_, err := j.Judge(context.Background(), "test", nil)
	if err == nil {
		t.Fatal("expected error for empty candidates, got nil")
	}
}

func TestHeuristicJudge_SingleCandidateAlwaysWins(t *testing.T) {
	j := NewHeuristicJudge()
	cands := []Candidate{{Index: 0, Text: "this is a normal candidate with text content"}}
	v, err := j.Judge(context.Background(), "test", cands)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.WinnerIndex != 0 {
		t.Errorf("winner = %d, want 0", v.WinnerIndex)
	}
}

func TestHeuristicJudge_CandidatesWithErrPenalized(t *testing.T) {
	j := NewHeuristicJudge()
	cands := []Candidate{
		{Index: 0, Text: "a perfectly normal candidate with enough text content"},
		{Index: 1, Err: errors.New("agent crashed")},
		{Index: 2, Text: "another perfectly normal candidate with text content"},
	}
	v, err := j.Judge(context.Background(), "test", cands)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.WinnerIndex == 1 {
		t.Errorf("expected the failed candidate (index 1) to be penalized, got winner %d", v.WinnerIndex)
	}
}

func TestHeuristicJudge_TestKeywordRewarded(t *testing.T) {
	j := NewHeuristicJudge()
	cands := []Candidate{
		{Index: 0, Text: "plain implementation, nothing special mentioned in this text"},
		{Index: 1, Text: "added test cases and spec coverage with enough text here for sure"},
	}
	v, err := j.Judge(context.Background(), "test", cands)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.WinnerIndex != 1 {
		t.Errorf("expected index 1 (mentions 'test' and 'spec') to win, got %d", v.WinnerIndex)
	}
}

func TestHeuristicJudge_LintFmtVetRewarded(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
	}{
		{"lint", "ran the linter on the code and fixed all the issues I could find"},
		{"fmt", "ran go fmt to fix the formatting and made the code much cleaner now"},
		{"vet", "ran go vet to ensure the static checks all pass and we are good to go"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			j := NewHeuristicJudge()
			cands := []Candidate{
				{Index: 0, Text: "plain implementation with no tool references in this text"},
				{Index: 1, Text: c.text},
			}
			v, err := j.Judge(context.Background(), "test", cands)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.WinnerIndex != 1 {
				t.Errorf("expected candidate with %q to win, got %d", c.name, v.WinnerIndex)
			}
		})
	}
}

func TestHeuristicJudge_EmptyTextPenalized(t *testing.T) {
	j := NewHeuristicJudge()
	cands := []Candidate{
		{Index: 0, Text: "this is a normal candidate with text content here"},
		{Index: 1, Text: ""},
		{Index: 2, Text: "this is another normal candidate with text content"},
	}
	v, err := j.Judge(context.Background(), "test", cands)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.WinnerIndex == 1 {
		t.Errorf("expected the empty candidate to be penalized, got winner %d", v.WinnerIndex)
	}
}

func TestHeuristicJudge_ShortTextPenalized(t *testing.T) {
	j := NewHeuristicJudge()
	short := "x"
	normal := strings.Repeat("alpha ", 20)
	cands := []Candidate{
		{Index: 0, Text: normal},
		{Index: 1, Text: short},
	}
	if len(short) >= 50 {
		t.Fatalf("short text must be <50 chars, got %d", len(short))
	}
	if len(normal) <= 50 {
		t.Fatalf("normal text must be >50 chars, got %d", len(normal))
	}
	v, err := j.Judge(context.Background(), "test", cands)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.WinnerIndex != 0 {
		t.Errorf("expected the long-text candidate to win, got %d", v.WinnerIndex)
	}
}

func TestHeuristicJudge_LongTextPenalized(t *testing.T) {
	j := NewHeuristicJudge()
	normal := strings.Repeat("x", 100)
	tooLong := strings.Repeat("x", 5000)
	if len(normal) >= 4000 {
		t.Fatalf("normal text must be <4000 chars, got %d", len(normal))
	}
	if len(tooLong) <= 4000 {
		t.Fatalf("long text must be >4000 chars, got %d", len(tooLong))
	}
	cands := []Candidate{
		{Index: 0, Text: normal},
		{Index: 1, Text: tooLong},
	}
	v, err := j.Judge(context.Background(), "test", cands)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.WinnerIndex != 0 {
		t.Errorf("expected the medium-length candidate to win, got %d", v.WinnerIndex)
	}
}

func TestHeuristicJudge_HighTokenPenalized(t *testing.T) {
	j := NewHeuristicJudge()
	// 3 candidates with same text so the only difference is usage.
	text := "candidate with enough text to avoid length penalty for the heuristic"
	cands := []Candidate{
		{Index: 0, Text: text, Usage: llm.Usage{Input: 100, Output: 50, Total: 150}},
		{Index: 1, Text: text, Usage: llm.Usage{Input: 100, Output: 50, Total: 150}},
		{Index: 2, Text: text, Usage: llm.Usage{Input: 10000, Output: 5000, Total: 15000}},
	}
	scores := make([]float64, len(cands))
	// Manually walk through the logic to capture per-candidate scores.
	median := tokenMedian(cands)
	for i, c := range cands {
		s := 0.5
		if c.Err != nil {
			s -= 0.4
		}
		lc := strings.ToLower(c.Text)
		if strings.Contains(lc, "test") || strings.Contains(lc, "spec") {
			s += 0.15
		}
		if strings.Contains(lc, "lint") || strings.Contains(lc, "fmt") || strings.Contains(lc, "vet") {
			s += 0.05
		}
		if len(c.Text) == 0 {
			s -= 0.2
		} else if len(c.Text) < 50 {
			s -= 0.1
		} else if len(c.Text) > 4000 {
			s -= 0.1
		}
		if median > 0 {
			ratio := float64(c.Usage.Total) / float64(median)
			if ratio > 1.5 {
				s -= 0.1
			} else if ratio < 0.5 {
				s += 0.05
			}
		}
		scores[i] = s
	}
	if !(scores[2] < scores[0]) {
		t.Fatalf("setup wrong: high-token score %f should be < normal %f", scores[2], scores[0])
	}

	v, err := j.Judge(context.Background(), "test", cands)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.WinnerIndex == 2 {
		t.Errorf("expected high-token candidate to be penalized, got winner %d (score %f)", v.WinnerIndex, v.Score)
	}
}

func TestHeuristicJudge_LowTokenRewarded(t *testing.T) {
	j := NewHeuristicJudge()
	text := "candidate with enough text to avoid length penalty for the heuristic"
	cands := []Candidate{
		{Index: 0, Text: text, Usage: llm.Usage{Input: 1, Output: 1, Total: 2}},
		{Index: 1, Text: text, Usage: llm.Usage{Input: 100, Output: 50, Total: 150}},
		{Index: 2, Text: text, Usage: llm.Usage{Input: 100, Output: 50, Total: 150}},
	}
	v, err := j.Judge(context.Background(), "test", cands)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.WinnerIndex != 0 {
		t.Errorf("expected low-token candidate (index 0) to win, got %d", v.WinnerIndex)
	}
}

func TestHeuristicJudge_ScoreClampedToUnitInterval(t *testing.T) {
	j := NewHeuristicJudge()
	// Pump score up: text contains "test spec lint fmt vet", length 50..4000, low token.
	bonus := "test spec lint fmt vet " // +0.15 +0.05
	// Need 3 candidates for median to be set.
	med := strings.Repeat(bonus+"plain long text here we go ", 10)
	cands := []Candidate{
		{Index: 0, Text: med, Usage: llm.Usage{Input: 1, Output: 1, Total: 2}},
		{Index: 1, Text: med, Usage: llm.Usage{Input: 100, Output: 50, Total: 150}},
		{Index: 2, Text: med, Usage: llm.Usage{Input: 100, Output: 50, Total: 150}},
	}
	v, err := j.Judge(context.Background(), "test", cands)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Score < 0 || v.Score > 1 {
		t.Errorf("score %f out of [0,1]", v.Score)
	}

	// Floor case: Err + empty text. Score must clamp to 0 (or >= 0).
	cands2 := []Candidate{
		{Index: 0, Err: errors.New("boom"), Text: ""},
		{Index: 1, Text: "a normal candidate with text content present here"},
		{Index: 2, Text: "another normal candidate with text content present"},
	}
	v2, err := j.Judge(context.Background(), "test", cands2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v2.Score < 0 || v2.Score > 1 {
		t.Errorf("score %f out of [0,1]", v2.Score)
	}
	if v2.WinnerIndex == 0 {
		t.Errorf("expected failed candidate to lose, got winner %d", v2.WinnerIndex)
	}
}

func TestHeuristicJudge_Deterministic(t *testing.T) {
	j := NewHeuristicJudge()
	cands := []Candidate{
		{Index: 0, Text: "plain implementation, with reasonable length here"},
		{Index: 1, Text: "added test cases plus a spec section, enough text length"},
		{Index: 2, Err: errors.New("oops")},
		{Index: 3, Text: strings.Repeat("x", 5000)},
	}
	v1, err := j.Judge(context.Background(), "the prompt", cands)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v2, err := j.Judge(context.Background(), "the prompt", cands)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v1.WinnerIndex != v2.WinnerIndex {
		t.Errorf("non-deterministic winner: v1=%d v2=%d", v1.WinnerIndex, v2.WinnerIndex)
	}
	if v1.Score != v2.Score {
		t.Errorf("non-deterministic score: v1=%f v2=%f", v1.Score, v2.Score)
	}
}

func TestHeuristicJudge_TieBrokenByLowerIndex(t *testing.T) {
	j := NewHeuristicJudge()
	text := strings.Repeat("alpha ", 20)
	cands := []Candidate{
		{Index: 0, Text: text},
		{Index: 1, Text: text},
		{Index: 2, Text: text},
		{Index: 3, Text: text},
	}
	v, err := j.Judge(context.Background(), "test", cands)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.WinnerIndex != 0 {
		t.Errorf("expected the lowest-index candidate to win ties, got %d", v.WinnerIndex)
	}
}
