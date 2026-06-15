package consult

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestConsultSelected_MixedSuccessAndFailure(t *testing.T) {
	provs := []llm.Provider{
		&stubProvider{name: "a", text: "answer A", inTok: 10, outTok: 20},
		&stubProvider{name: "broken", failStrm: true},
		&stubProvider{name: "c", text: "answer C", inTok: 10, outTok: 15},
	}
	c := &Council{Judge: &judgeStub{verdictText: `{"winner": 2, "reason": "C wins"}`}}
	res, err := c.ConsultSelected(context.Background(), "q?", provs)
	if err != nil {
		t.Fatalf("ConsultSelected: %v", err)
	}
	// Full roster preserved, failure included in order.
	if len(res.Candidates) != 3 {
		t.Fatalf("candidates = %d, want 3 (failures included)", len(res.Candidates))
	}
	if res.Candidates[1].Err == nil {
		t.Error("candidate 1 should carry its error")
	}
	if res.Candidates[0].Index != 1 || res.Candidates[2].Index != 3 {
		t.Errorf("indices = %d,%d, want 1,3", res.Candidates[0].Index, res.Candidates[2].Index)
	}
	// Judge picked good#2 (= "c") which is full-roster index 2.
	if res.Verdict.WinnerIndex != 2 {
		t.Errorf("WinnerIndex = %d, want 2 (mapped to full slice)", res.Verdict.WinnerIndex)
	}
	if !strings.Contains(res.Verdict.Reason, "C wins") {
		t.Errorf("Reason = %q", res.Verdict.Reason)
	}
	if res.TotalTokens != 10+20+10+15 {
		t.Errorf("TotalTokens = %d", res.TotalTokens)
	}
}

func TestConsultSelected_AllFailed(t *testing.T) {
	provs := []llm.Provider{
		&stubProvider{name: "x", failInit: true},
		&stubProvider{name: "y", failStrm: true},
	}
	c := &Council{Judge: &judgeStub{verdictText: `{"winner": 1, "reason": "n/a"}`}}
	res, err := c.ConsultSelected(context.Background(), "q?", provs)
	if err != nil {
		t.Fatalf("ConsultSelected: %v", err)
	}
	if !res.AllFailed {
		t.Error("AllFailed should be true")
	}
	if res.Verdict.WinnerIndex != -1 {
		t.Errorf("WinnerIndex = %d, want -1", res.Verdict.WinnerIndex)
	}
	if len(res.Candidates) != 2 {
		t.Errorf("candidates = %d, want 2 (errors kept)", len(res.Candidates))
	}
}

func TestConsultSelected_SingleSuccessSkipsJudge(t *testing.T) {
	calls := new(int32)
	provs := []llm.Provider{
		&stubProvider{name: "bad", failStrm: true},
		&stubProvider{name: "good", text: "only answer", inTok: 1, outTok: 2},
	}
	c := &Council{Judge: &judgeStub{verdictText: `{"winner": 1, "reason": "x"}`, calls: calls}}
	res, err := c.ConsultSelected(context.Background(), "q?", provs)
	if err != nil {
		t.Fatalf("ConsultSelected: %v", err)
	}
	if *calls != 0 {
		t.Errorf("judge calls = %d, want 0", *calls)
	}
	if res.Verdict.WinnerIndex != 1 {
		t.Errorf("WinnerIndex = %d, want 1", res.Verdict.WinnerIndex)
	}
}

func TestConsultSelected_EmptyInputs(t *testing.T) {
	c := &Council{}
	if _, err := c.ConsultSelected(context.Background(), "", []llm.Provider{&stubProvider{name: "a"}}); err == nil {
		t.Error("empty question should error")
	}
	if _, err := c.ConsultSelected(context.Background(), "q", nil); err == nil {
		t.Error("empty provider set should error")
	}
}
