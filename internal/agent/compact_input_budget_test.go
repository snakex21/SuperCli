package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// windowProvider rejects every request whose real size (our estimate times
// ratio: the estimator undercounts) exceeds limit, as Anthropic does:
// "prompt is too long: X tokens > Y maximum". Summary requests get a summary,
// other requests an answer.
type windowProvider struct {
	name        string
	limit       int
	ratio       float64
	mu          sync.Mutex
	calls       int
	rejected    int
	summaries   int
	maxAccepted int
}

func (p *windowProvider) Name() string { return p.name }

func (p *windowProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	real := int(float64(llm.EstimateTokens(msgs)) * p.ratio)
	p.mu.Lock()
	p.calls++
	if real > p.limit {
		p.rejected++
		p.mu.Unlock()
		return nil, fmt.Errorf(`http 400: {"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: %d tokens > %d maximum"}}`, real, p.limit)
	}
	p.maxAccepted = max(p.maxAccepted, real)
	summary := len(msgs) > 0 && msgs[0].Role == llm.RoleSystem && strings.Contains(msgs[0].Content, "Summarize the conversation")
	if summary {
		p.summaries++
	}
	p.mu.Unlock()
	text := "Answer: continuing the Minecraft work."
	if summary {
		text = "Goal: finish the survival night.\nDone: mobs added.\nState: build passes.\nPending: zombie spawn."
	}
	ch := make(chan llm.Delta, 1)
	ch <- llm.Delta{Content: text, FinishReason: "stop"}
	close(ch)
	return ch, nil
}

// bigHistory: worker reports, tool calls and long tool outputs, as in the
// reported session (thousands of messages, never compacted).
func bigHistory(turns int) []llm.Message {
	var msgs []llm.Message
	for i := 0; i < turns; i++ {
		id := fmt.Sprintf("call-%d", i)
		msgs = append(msgs,
			llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("<task-notification><task-id>worker-%d</task-id> report: %s</task-notification>", i, strings.Repeat("zombie spawn evidence ", 300))},
			llm.Message{Role: llm.RoleAssistant, Content: "Checking the build.", ToolCalls: []llm.ToolCall{{ID: id, Name: "ctx_execute", Arguments: `{"command":["gradlew","build"],"note":"` + strings.Repeat("arg ", 300) + `"}`}}},
			llm.Message{Role: llm.RoleTool, Name: "ctx_execute", ToolCallID: id, Content: strings.Repeat("BUILD line output\n", 2000)},
			llm.Message{Role: llm.RoleAssistant, Content: "Build checked."},
		)
	}
	return msgs
}

// A history far above the window compacts: tighter caps and a rolling summary
// over chunks keep every request within the provider's limit.
func TestOversizedHistoryCompactsWithinTheWindow(t *testing.T) {
	const window = 40_000
	msgs := bigHistory(200)
	if est := llm.EstimateTokens(msgs); est < 5*window {
		t.Fatalf("setup: history estimate %d is not far above the window", est)
	}
	p := &windowProvider{name: "test-window-chunks", limit: window, ratio: 1.3}
	var notices []string
	ctx := withCompactNotice(WithCompactWindow(context.Background(), window), func(s string) { notices = append(notices, s) })
	summary, err := NewAutoSummarizer(nil)(ctx, p, msgs)
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	if !strings.Contains(summary, "zombie spawn") || !strings.HasPrefix(summary, compactSummaryPreamble) {
		t.Fatalf("summary = %.200q", summary)
	}
	if p.rejected != 0 || p.maxAccepted > window {
		t.Fatalf("a summary request exceeded the window: rejected=%d max=%d", p.rejected, p.maxAccepted)
	}
	if p.summaries < 2 || len(notices) != 1 || !strings.Contains(notices[0], "summarizing it in") {
		t.Fatalf("expected a chunked summary with one notice: summaries=%d notices=%q", p.summaries, notices)
	}
}

// Moderately oversized input fits after eliding old tool output: one request.
func TestCompactionElidesBeforeChunking(t *testing.T) {
	msgs := bigHistory(40)
	normal := estimateTranscriptTokens(compactSummaryInput(msgs))
	elided := estimateTranscriptTokens(renderCompactTranscriptCaps(msgs, autoCompactSplit(msgs), elidedCompactCaps)) +
		estimateTranscriptTokens(compactionStatePrompt)
	window := 2*elided + 1000
	if normal <= window/2 {
		t.Fatalf("setup: normal summary input %d fits half the window %d", normal, window)
	}
	p := &windowProvider{name: "test-window-elide", limit: window, ratio: 1}
	if _, err := NewAutoSummarizer(nil)(WithCompactWindow(context.Background(), window), p, msgs); err != nil {
		t.Fatal(err)
	}
	if p.summaries != 1 || p.rejected != 0 {
		t.Fatalf("summaries=%d rejected=%d, want one elided request", p.summaries, p.rejected)
	}
}

// "prompt is too long: X > Y" is parsed; the estimator's undercount is
// learned and the summary is retried smaller until it fits.
func TestPromptTooLongIsParsedAndRetriedSmaller(t *testing.T) {
	const window = 40_000
	p := &windowProvider{name: "test-window-retry", limit: window, ratio: 2.6}
	summary, err := NewAutoSummarizer(nil)(WithCompactWindow(context.Background(), window), p, bigHistory(60))
	if err != nil {
		t.Fatalf("compaction failed after retries: %v", err)
	}
	if summary == "" || p.rejected == 0 {
		t.Fatalf("expected at least one rejected attempt before success: rejected=%d", p.rejected)
	}
	if r := learnedEstimateRatio("test-window-retry"); r < 2 {
		t.Fatalf("learned estimate ratio = %.2f, want about 2.6", r)
	}
	real, limit, ok := parsePromptTooLong(fmt.Errorf(`http 400: {"message":"prompt is too long: 1295213 tokens > 1000000 maximum"}`))
	if !ok || real != 1295213 || limit != 1000000 {
		t.Fatalf("parse = %d, %d, %v", real, limit, ok)
	}
	if got := extractContextLimit("prompt is too long: 1295213 tokens > 1000000 maximum"); got != 1000000 {
		t.Fatalf("extractContextLimit = %d", got)
	}
	if !isContextLimitErr(fmt.Errorf("prompt is too long: 1295213 tokens > 1000000 maximum")) {
		t.Fatal("prompt is too long is not a context-limit error")
	}
}

// The reported state: a session whose history is above the window and whose
// compaction used to fail. The next message compacts within the window and
// gets an answer; the archived transcript keeps every message.
func TestStuckOversizedSessionRecoversOnNextMessage(t *testing.T) {
	const window = 60_000
	history := bigHistory(120)
	p := &windowProvider{name: "test-window-stuck", limit: window, ratio: 1.3}
	l, err := NewLoop(LoopConfig{
		Provider: p, Registry: tools.NewRegistry(), System: "You build a Minecraft mod.",
		InitialMessages: history, MaxSteps: 3, PruneProtectTokens: -1,
		Summarizer: NewAutoSummarizer(nil),
		WindowFor:  func(string) int { return window },
	})
	if err != nil {
		t.Fatal(err)
	}
	l.contextModel = contextModelState{loaded: true, provider: l.prefillScope(), model: l.modelID}
	ch, err := l.Run(context.Background(), "limit wrócił, kontynuuj")
	if err != nil {
		t.Fatal(err)
	}
	var answer strings.Builder
	compacted := false
	for ev := range ch {
		switch e := ev.(type) {
		case ErrorEvent:
			t.Fatalf("turn failed: %v", e.Err)
		case AutoCompactEvent:
			compacted = true
		case MessageEvent:
			answer.WriteString(e.Text)
		}
	}
	if !compacted || !strings.Contains(answer.String(), "continuing the Minecraft work") {
		t.Fatalf("compacted=%v answer=%q rejected=%d", compacted, answer.String(), p.rejected)
	}
	if p.maxAccepted > window {
		t.Fatalf("an accepted request exceeded the window: %d", p.maxAccepted)
	}
	if got := len(l.compactionHistory().messages); got == 0 {
		t.Fatal("no history left")
	}
}

// The main request is under the threshold by our estimate but too long for
// the provider: the error is recognized, the history compacts, the turn
// continues, and the model's undercount is learned for the next estimates.
func TestMainRequestPromptTooLongCompactsAndRetries(t *testing.T) {
	const window = 60_000
	history := bigHistory(4)
	est := llm.EstimateTokens(history)
	if est >= autoCompactThreshold(window) || int(float64(est)*1.6) <= window {
		t.Fatalf("setup: estimate %d must be under the threshold %d but above the window when real", est, autoCompactThreshold(window))
	}
	p := &windowProvider{name: "test-window-main", limit: window, ratio: 1.6}
	l, err := NewLoop(LoopConfig{
		Provider: p, Registry: tools.NewRegistry(), System: "You build a Minecraft mod.",
		InitialMessages: history, MaxSteps: 3, PruneProtectTokens: -1,
		Summarizer: NewAutoSummarizer(nil),
		WindowFor:  func(string) int { return window },
	})
	if err != nil {
		t.Fatal(err)
	}
	l.contextModel = contextModelState{loaded: true, provider: l.prefillScope(), model: l.modelID}
	ch, err := l.Run(context.Background(), "kontynuuj")
	if err != nil {
		t.Fatal(err)
	}
	var answer strings.Builder
	for ev := range ch {
		switch e := ev.(type) {
		case ErrorEvent:
			t.Fatalf("turn failed: %v (rejected=%d)", e.Err, p.rejected)
		case MessageEvent:
			answer.WriteString(e.Text)
		}
	}
	if p.rejected == 0 || !strings.Contains(answer.String(), "continuing") {
		t.Fatalf("rejected=%d answer=%q", p.rejected, answer.String())
	}
	if r := learnedEstimateRatio(l.modelID); r < 1.3 {
		t.Fatalf("learned ratio for %s = %.2f", l.modelID, r)
	}
}
