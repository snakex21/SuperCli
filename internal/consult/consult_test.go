package consult

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
)

// stubProvider is the minimal llm.Provider
// implementation used by consult tests. It
// returns its canned response on a buffered
// channel, optionally with usage accounting and
// an optional delay (for concurrency tests).
type stubProvider struct {
	name     string
	text     string
	inTok    int
	outTok   int
	delay    time.Duration
	failInit bool // return err from Complete
	failStrm bool // send Delta{Err: ...} then close
	calls    *int32
	mu       sync.Mutex
	lastMsgs []llm.Message
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	if s.calls != nil {
		atomic.AddInt32(s.calls, 1)
	}
	s.mu.Lock()
	s.lastMsgs = msgs
	s.mu.Unlock()
	if s.failInit {
		return nil, errors.New("stub: init fail")
	}
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			ch := make(chan llm.Delta, 1)
			ch <- llm.Delta{Err: ctx.Err()}
			close(ch)
			return ch, nil
		}
	}
	ch := make(chan llm.Delta, 4)
	if s.failStrm {
		ch <- llm.Delta{Err: errors.New("stub: stream fail")}
		close(ch)
		return ch, nil
	}
	ch <- llm.Delta{Content: s.text}
	ch <- llm.Delta{
		FinishReason: "stop",
		Usage:        &llm.Usage{Input: s.inTok, Output: s.outTok, Total: s.inTok + s.outTok},
	}
	close(ch)
	return ch, nil
}

// judgeStub is a stub LLM provider that always
// returns the canned JSON verdict. The
// `verdictText` is what the judge "says"; the
// test sets it to the expected output.
type judgeStub struct {
	verdictText string
	calls       *int32
}

func (j *judgeStub) Name() string { return "judge-stub" }

func (j *judgeStub) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	if j.calls != nil {
		atomic.AddInt32(j.calls, 1)
	}
	ch := make(chan llm.Delta, 2)
	ch <- llm.Delta{Content: j.verdictText}
	ch <- llm.Delta{FinishReason: "stop"}
	close(ch)
	return ch, nil
}

func TestConsult_HappyPath_ThreeSamplesJudgePicks(t *testing.T) {
	samples := []*stubProvider{
		{name: "a", text: "answer A", inTok: 10, outTok: 20},
		{name: "b", text: "answer B is best", inTok: 10, outTok: 25},
		{name: "c", text: "answer C", inTok: 10, outTok: 15},
	}
	provs := make([]llm.Provider, len(samples))
	for i, s := range samples {
		provs[i] = s
	}
	calls := new(int32)
	judge := &judgeStub{
		verdictText: `{"winner": 2, "reason": "B is the most complete"}`,
		calls:       calls,
	}
	c := &Council{
		Samples: provs,
		Judge:   judge,
	}
	res, err := c.Consult(context.Background(), Request{Question: "what is 2+2?"})
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if res.AllFailed {
		t.Fatal("AllFailed should be false")
	}
	if len(res.Candidates) != 3 {
		t.Errorf("candidates = %d, want 3", len(res.Candidates))
	}
	// WinnerIndex 1 = 0-based index of candidate 2 (B)
	if res.Verdict.WinnerIndex != 1 {
		t.Errorf("WinnerIndex = %d, want 1", res.Verdict.WinnerIndex)
	}
	if !strings.Contains(res.Verdict.Reason, "complete") {
		t.Errorf("Reason = %q, want to contain 'complete'", res.Verdict.Reason)
	}
	if res.TotalTokens != 10+20+10+25+10+15 {
		t.Errorf("TotalTokens = %d, want sum of samples (judge has 0)", res.TotalTokens)
	}
	if *calls != 1 {
		t.Errorf("judge calls = %d, want 1", *calls)
	}
}

func TestConsult_PartialFailure(t *testing.T) {
	// Sample 0 errors, samples 1 and 2 succeed.
	// Judge sees 2 candidates, picks #2.
	samples := []*stubProvider{
		{name: "broken", failStrm: true},
		{name: "ok-1", text: "first ok", outTok: 10},
		{name: "ok-2", text: "second ok", outTok: 12},
	}
	provs := make([]llm.Provider, len(samples))
	for i, s := range samples {
		provs[i] = s
	}
	judge := &judgeStub{verdictText: `{"winner": 2, "reason": "second is more concise"}`}
	c := &Council{Samples: provs, Judge: judge}
	res, err := c.Consult(context.Background(), Request{Question: "q"})
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if res.AllFailed {
		t.Fatal("AllFailed should be false")
	}
	if len(res.Candidates) != 2 {
		t.Errorf("candidates = %d, want 2 (broken filtered out)", len(res.Candidates))
	}
	// Candidate Index should be 1-based and refer to ORIGINAL positions
	// (1=broken, 2=ok-1, 3=ok-2). After filtering we keep ok-1=2, ok-2=3.
	if res.Candidates[0].Index != 2 || res.Candidates[1].Index != 3 {
		t.Errorf("candidate indices = [%d, %d], want [2, 3] (1-based original positions)",
			res.Candidates[0].Index, res.Candidates[1].Index)
	}
}

func TestConsult_AllFailed(t *testing.T) {
	samples := []*stubProvider{
		{name: "a", failStrm: true},
		{name: "b", failStrm: true},
	}
	provs := make([]llm.Provider, len(samples))
	for i, s := range samples {
		provs[i] = s
	}
	judge := &judgeStub{verdictText: `{"winner": 1, "reason": "x"}`}
	c := &Council{Samples: provs, Judge: judge}
	res, err := c.Consult(context.Background(), Request{Question: "q"})
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if !res.AllFailed {
		t.Errorf("AllFailed should be true when all samples fail")
	}
	if len(res.Candidates) != 0 {
		t.Errorf("candidates = %d, want 0", len(res.Candidates))
	}
}

func TestConsult_OneSuccess_SkipsJudge(t *testing.T) {
	samples := []*stubProvider{
		{name: "ok", text: "lonely answer", outTok: 5},
		{name: "bad", failStrm: true},
		{name: "bad2", failStrm: true},
	}
	provs := make([]llm.Provider, len(samples))
	for i, s := range samples {
		provs[i] = s
	}
	calls := new(int32)
	judge := &judgeStub{verdictText: `{"winner": 1, "reason": "x"}`, calls: calls}
	c := &Council{Samples: provs, Judge: judge}
	res, err := c.Consult(context.Background(), Request{Question: "q", N: 3})
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if len(res.Candidates) != 1 {
		t.Errorf("candidates = %d, want 1", len(res.Candidates))
	}
	if res.Verdict.WinnerIndex != 0 {
		t.Errorf("WinnerIndex = %d, want 0 (only candidate)", res.Verdict.WinnerIndex)
	}
	if !strings.Contains(res.Verdict.Reason, "only one") {
		t.Errorf("Reason = %q, want 'only one'", res.Verdict.Reason)
	}
	if *calls != 0 {
		t.Errorf("judge should be skipped when only 1 candidate, got %d calls", *calls)
	}
}

func TestConsult_EmptyQuestion(t *testing.T) {
	c := &Council{
		Samples: []llm.Provider{&stubProvider{name: "a", text: "x"}},
		Judge:   &judgeStub{verdictText: `{"winner": 1, "reason": "x"}`},
	}
	_, err := c.Consult(context.Background(), Request{Question: ""})
	if err == nil {
		t.Fatal("empty question should error")
	}
}

func TestConsult_NoJudge(t *testing.T) {
	c := &Council{
		Samples: []llm.Provider{&stubProvider{name: "a", text: "x"}},
		Judge:   nil,
	}
	_, err := c.Consult(context.Background(), Request{Question: "q"})
	if err == nil {
		t.Fatal("no judge should error")
	}
}

func TestConsult_NoSamples(t *testing.T) {
	c := &Council{
		Samples: nil,
		Judge:   &judgeStub{verdictText: `{"winner": 1, "reason": "x"}`},
	}
	_, err := c.Consult(context.Background(), Request{Question: "q"})
	if err == nil {
		t.Fatal("no samples should error")
	}
}

func TestConsult_NClamping(t *testing.T) {
	// N=10 with only 2 samples → clamps to 2.
	samples := []*stubProvider{
		{name: "a", text: "x", outTok: 1},
		{name: "b", text: "y", outTok: 1},
	}
	provs := make([]llm.Provider, len(samples))
	for i, s := range samples {
		provs[i] = s
	}
	calls := new(int32)
	judge := &judgeStub{verdictText: `{"winner": 1, "reason": "ok"}`, calls: calls}
	c := &Council{Samples: provs, Judge: judge}
	res, err := c.Consult(context.Background(), Request{Question: "q", N: 10})
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if len(res.Candidates) != 2 {
		t.Errorf("candidates = %d, want 2 (clamped)", len(res.Candidates))
	}
	// n=0 means "use all" too
	_, err = c.Consult(context.Background(), Request{Question: "q", N: 0})
	if err != nil {
		t.Errorf("N=0 (use all) failed: %v", err)
	}
}

func TestConsult_CtxCancelPropagates(t *testing.T) {
	// Long-delay samples that should bail out on
	// ctx.Done. The judge should NOT be called.
	samples := []*stubProvider{
		{name: "slow-1", text: "x", delay: 200 * time.Millisecond},
		{name: "slow-2", text: "y", delay: 200 * time.Millisecond},
	}
	provs := make([]llm.Provider, len(samples))
	for i, s := range samples {
		provs[i] = s
	}
	calls := new(int32)
	judge := &judgeStub{verdictText: `{"winner": 1, "reason": "ok"}`, calls: calls}
	c := &Council{Samples: provs, Judge: judge}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	// With ctx canceled early, both samples
	// should fail; consult should return
	// AllFailed=true. The judge MUST NOT be
	// called.
	start := time.Now()
	res, err := c.Consult(ctx, Request{Question: "q"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if !res.AllFailed {
		t.Errorf("AllFailed should be true after ctx cancel")
	}
	if *calls != 0 {
		t.Errorf("judge should NOT be called when all samples fail, got %d", *calls)
	}
	// Sanity: we shouldn't have waited for the
	// full 200ms sleep.
	if elapsed > 150*time.Millisecond {
		t.Errorf("Consult took %v, want < 150ms (samples should bail on ctx)", elapsed)
	}
}

func TestConsult_JudgeMalformedResponse(t *testing.T) {
	// Judge returns garbage. The candidates
	// should still be returned, but the verdict
	// is a fallback string.
	samples := []*stubProvider{
		{name: "a", text: "x", outTok: 1},
		{name: "b", text: "y", outTok: 1},
	}
	provs := make([]llm.Provider, len(samples))
	for i, s := range samples {
		provs[i] = s
	}
	judge := &judgeStub{verdictText: "this is not JSON at all"}
	c := &Council{Samples: provs, Judge: judge}
	res, err := c.Consult(context.Background(), Request{Question: "q"})
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if len(res.Candidates) != 2 {
		t.Errorf("candidates = %d, want 2 (judge failure should NOT lose candidates)", len(res.Candidates))
	}
	if !strings.Contains(res.Verdict.Reason, "judge failed") {
		t.Errorf("Reason = %q, want 'judge failed'", res.Verdict.Reason)
	}
}

func TestConsult_JudgeCodeFence(t *testing.T) {
	// ```json { ... } ``` is the common
	// wrapping; parser must accept it.
	samples := []*stubProvider{
		{name: "a", text: "x", outTok: 1},
		{name: "b", text: "y", outTok: 1},
	}
	provs := make([]llm.Provider, len(samples))
	for i, s := range samples {
		provs[i] = s
	}
	judge := &judgeStub{verdictText: "```json\n{\"winner\": 1, \"reason\": \"a is fine\"}\n```"}
	c := &Council{Samples: provs, Judge: judge}
	res, err := c.Consult(context.Background(), Request{Question: "q"})
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if res.Verdict.WinnerIndex != 0 {
		t.Errorf("WinnerIndex = %d, want 0 (winner=1 → 0-based 0)", res.Verdict.WinnerIndex)
	}
	if !strings.Contains(res.Verdict.Reason, "fine") {
		t.Errorf("Reason = %q, want 'fine'", res.Verdict.Reason)
	}
}

func TestConsult_JudgeWinnerOutOfRange(t *testing.T) {
	samples := []*stubProvider{
		{name: "a", text: "x", outTok: 1},
		{name: "b", text: "y", outTok: 1},
	}
	provs := make([]llm.Provider, len(samples))
	for i, s := range samples {
		provs[i] = s
	}
	judge := &judgeStub{verdictText: `{"winner": 7, "reason": "x"}`}
	c := &Council{Samples: provs, Judge: judge}
	res, err := c.Consult(context.Background(), Request{Question: "q"})
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if !strings.Contains(res.Verdict.Reason, "judge failed") {
		t.Errorf("out-of-range winner should fall back, got Reason=%q", res.Verdict.Reason)
	}
}

func TestConsult_Parallel(t *testing.T) {
	// 5 samples with 100ms delay each; if Consult
	// fans out correctly the total time is ~100ms
	// rather than ~500ms.
	samples := make([]*stubProvider, 5)
	for i := range samples {
		samples[i] = &stubProvider{
			name:   string(rune('a' + i)),
			text:   "answer",
			outTok: 1,
			delay:  100 * time.Millisecond,
		}
	}
	provs := make([]llm.Provider, len(samples))
	for i, s := range samples {
		provs[i] = s
	}
	judge := &judgeStub{verdictText: `{"winner": 1, "reason": "ok"}`}
	c := &Council{Samples: provs, Judge: judge}
	start := time.Now()
	_, err := c.Consult(context.Background(), Request{Question: "q"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("Consult took %v, want < 300ms (5x100ms samples should parallelise to ~100ms)", elapsed)
	}
}

func TestConsult_TokenAccountingPerCandidate(t *testing.T) {
	// Verify each candidate carries its own In/Out.
	samples := []*stubProvider{
		{name: "a", text: "x", inTok: 50, outTok: 100},
		{name: "b", text: "y", inTok: 30, outTok: 80},
		{name: "c", text: "z", inTok: 40, outTok: 60},
	}
	provs := make([]llm.Provider, len(samples))
	for i, s := range samples {
		provs[i] = s
	}
	judge := &judgeStub{verdictText: `{"winner": 1, "reason": "x"}`}
	c := &Council{Samples: provs, Judge: judge}
	res, _ := c.Consult(context.Background(), Request{Question: "q"})
	wantIn := []int64{50, 30, 40}
	wantOut := []int64{100, 80, 60}
	for i, cd := range res.Candidates {
		if cd.In != wantIn[i] {
			t.Errorf("cand %d In = %d, want %d", i, cd.In, wantIn[i])
		}
		if cd.Out != wantOut[i] {
			t.Errorf("cand %d Out = %d, want %d", i, cd.Out, wantOut[i])
		}
	}
}

// judgeParseErrors feed straight to parseJudgeVerdict
// for unit tests of the parser itself.
func TestParseJudgeVerdict_PlainJSON(t *testing.T) {
	v, err := parseJudgeVerdict(`{"winner": 2, "reason": "two is good"}`, 3)
	if err != nil {
		t.Fatal(err)
	}
	if v.WinnerIndex != 1 {
		t.Errorf("WinnerIndex = %d, want 1", v.WinnerIndex)
	}
	if v.Reason != "two is good" {
		t.Errorf("Reason = %q", v.Reason)
	}
}

func TestParseJudgeVerdict_CodeFence(t *testing.T) {
	v, err := parseJudgeVerdict("```json\n{\"winner\": 1, \"reason\": \"a\"}\n```", 2)
	if err != nil {
		t.Fatal(err)
	}
	if v.WinnerIndex != 0 {
		t.Errorf("WinnerIndex = %d, want 0", v.WinnerIndex)
	}
}

func TestParseJudgeVerdict_ProseAroundJSON(t *testing.T) {
	v, err := parseJudgeVerdict("Sure! My pick is: {\"winner\": 1, \"reason\": \"a\"}.", 1)
	if err != nil {
		t.Fatal(err)
	}
	if v.WinnerIndex != 0 {
		t.Errorf("WinnerIndex = %d, want 0", v.WinnerIndex)
	}
}

func TestParseJudgeVerdict_EmptyResponse(t *testing.T) {
	_, err := parseJudgeVerdict("", 3)
	if err == nil {
		t.Error("empty should error")
	}
}

func TestParseJudgeVerdict_OutOfRange(t *testing.T) {
	_, err := parseJudgeVerdict(`{"winner": 5, "reason": "x"}`, 3)
	if err == nil {
		t.Error("out-of-range should error")
	}
}

func TestParseJudgeVerdict_MalformedJSON(t *testing.T) {
	_, err := parseJudgeVerdict("not json", 1)
	if err == nil {
		t.Error("malformed should error")
	}
}
