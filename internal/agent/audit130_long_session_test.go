package agent

// Audit-130: long agent session on an in-process fake provider. Drives the
// real loop (tool calls, pruning, production auto-summarizer) for 200 user
// turns with growing history and checks, on EVERY provider request:
//   - the leading system prompt is preserved verbatim,
//   - tool-call / tool-result pairing is intact (no orphan results, no
//     unanswered calls, no duplicate results),
//   - the request never exceeds the context window,
//   - a pinned requirement from turn 1 survives compaction,
// and samples heap (after GC) and goroutines over time.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

const (
	a130PinSystem      = "AUDIT-PIN-SYSTEM: you are the long-session audit agent."
	a130PinRequirement = "PINNED-REQUIREMENT-7f3a: every answer must end with the word OK."
)

type a130SessionProvider struct {
	mu           sync.Mutex
	window       int
	calls        int
	compactCalls int
	maxReq       int
	violations   []string
	pinMissing   int
	sawSummary   bool
	afterCompact bool
}

func (p *a130SessionProvider) Name() string         { return "audit130-session" }
func (p *a130SessionProvider) SupportsVision() bool { return false }

func (p *a130SessionProvider) violate(format string, args ...any) {
	if len(p.violations) < 20 {
		p.violations = append(p.violations, fmt.Sprintf(format, args...))
	}
}

func (p *a130SessionProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch := make(chan llm.Delta, 4)
	send := func(ds ...llm.Delta) (<-chan llm.Delta, error) {
		for _, d := range ds {
			ch <- d
		}
		close(ch)
		return ch, nil
	}
	usage := &llm.Usage{Input: llm.EstimateTokens(msgs), Output: 20, Total: llm.EstimateTokens(msgs) + 20}
	if llm.PurposeFromContext(ctx) == llm.PurposeCompact {
		p.compactCalls++
		transcript := ""
		for _, m := range msgs {
			transcript += m.Content
		}
		summary := "## Task state\nLong-session audit; earlier turns ran probe twice each and answered."
		if strings.Contains(transcript, a130PinRequirement) {
			summary += "\n" + a130PinRequirement
		}
		p.afterCompact = true
		return send(llm.Delta{Content: summary}, llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 100, Output: 30, Total: 130}})
	}
	p.calls++
	p.check(msgs)

	// Decide the next action from the tail of the request.
	last := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != llm.RoleSystem {
			last = i
			break
		}
	}
	turn, tools := 0, 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleTool {
			tools++
			continue
		}
		if msgs[i].Role == llm.RoleUser && strings.Contains(msgs[i].Content, "turn ") {
			f := strings.Fields(msgs[i].Content[strings.Index(msgs[i].Content, "turn "):])
			if len(f) > 1 {
				turn, _ = strconv.Atoi(strings.TrimRight(f[1], ".:"))
			}
			break
		}
	}
	if last >= 0 && (msgs[last].Role == llm.RoleUser || msgs[last].Role == llm.RoleTool) && tools < 2 {
		id := fmt.Sprintf("call_t%d_%d", turn, tools)
		args := fmt.Sprintf(`{"q":"turn %d step %d"}`, turn, tools)
		return send(llm.Delta{OutputStarted: true}, llm.Delta{ToolCall: &llm.ToolCall{ID: id, Name: "probe", Arguments: args}},
			llm.Delta{FinishReason: "tool_calls", Usage: usage})
	}
	return send(llm.Delta{Content: fmt.Sprintf("Turn %d finished. OK", turn)}, llm.Delta{FinishReason: "stop", Usage: usage})
}

func (p *a130SessionProvider) check(msgs []llm.Message) {
	if len(msgs) == 0 || msgs[0].Role != llm.RoleSystem || !strings.Contains(msgs[0].Content, a130PinSystem) {
		p.violate("call %d: leading system prompt missing/altered", p.calls)
	}
	calls := map[string]int{}
	results := map[string]int{}
	for i, m := range msgs {
		if m.Role == llm.RoleAssistant {
			for _, c := range m.ToolCalls {
				calls[c.ID] = i
			}
		}
		if m.Role == llm.RoleTool {
			if _, ok := calls[m.ToolCallID]; !ok {
				p.violate("call %d: orphan tool result %q at %d", p.calls, m.ToolCallID, i)
			}
			results[m.ToolCallID]++
			if results[m.ToolCallID] > 1 {
				p.violate("call %d: duplicate tool result %q", p.calls, m.ToolCallID)
			}
		}
	}
	for id := range calls {
		if results[id] == 0 {
			p.violate("call %d: tool call %q has no result", p.calls, id)
		}
	}
	est := llm.EstimateTokens(msgs)
	p.maxReq = max(p.maxReq, est)
	if est > p.window {
		p.violate("call %d: request estimate %d exceeds window %d", p.calls, est, p.window)
	}
	all := ""
	for _, m := range msgs {
		all += m.Content
		if strings.Contains(m.Content, "## Task state") {
			p.sawSummary = true
		}
	}
	if !strings.Contains(all, a130PinRequirement) {
		p.pinMissing++
	}
}

type a130Sample struct {
	turn                     int
	heapKB, goroutines       int
	messages, visibleTokens  int
	compactions, prunes, rpc int
	retained                 int
}

func a130LongSession(t *testing.T, turns int) {
	const window = 32000
	p := &a130SessionProvider{window: window}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "probe", Description: "probe the workspace",
		Schema: `{"type":"object","properties":{"q":{"type":"string"}}}`,
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			var a struct{ Q string }
			_ = json.Unmarshal(args, &a)
			return tools.Result{Text: strings.Repeat("probe output for "+a.Q+": lorem ipsum dolor sit amet\n", 40)}, nil
		}})
	reg.MarkAlwaysOn("probe")
	l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, System: a130PinSystem, MaxSteps: 10,
		WindowFor: func(string) int { return window }, Summarizer: NewAutoSummarizer(nil)})
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	baseG := runtime.NumGoroutine()
	var samples []a130Sample
	compactions, prunes := 0, 0
	var compactEvents []AutoCompactEvent
	start := time.Now()
	for turn := 1; turn <= turns; turn++ {
		prompt := fmt.Sprintf("Please handle turn %d: run probe twice, then answer.", turn)
		if turn == 1 {
			prompt += " " + a130PinRequirement
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		ch, err := l.Run(ctx, prompt)
		if err != nil {
			cancel()
			t.Fatalf("turn %d: %v", turn, err)
		}
		done := false
		for ev := range ch {
			switch e := ev.(type) {
			case DoneEvent:
				done = true
			case ErrorEvent:
				t.Fatalf("turn %d: run error: %v", turn, e.Err)
			case AutoCompactEvent:
				compactions++
				compactEvents = append(compactEvents, e)
			case ToolResultsPrunedEvent:
				prunes++
			}
		}
		cancel()
		if !done {
			t.Fatalf("turn %d: no DoneEvent", turn)
		}
		if turn%20 == 0 || turn == 1 {
			runtime.GC()
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			p.mu.Lock()
			rpc := p.calls
			p.mu.Unlock()
			samples = append(samples, a130Sample{turn: turn, heapKB: int(ms.HeapAlloc >> 10), goroutines: runtime.NumGoroutine() - baseG,
				messages: len(l.Messages), visibleTokens: llm.EstimateTokens(l.VisibleMessages()), compactions: compactions, prunes: prunes, rpc: rpc, retained: a130RetainedTokens(l.Messages)})
		}
	}
	elapsed := time.Since(start)
	t.Logf("%d turns in %s; provider calls=%d compaction calls=%d maxRequestTokens=%d (window %d, auto threshold %d)", turns, elapsed, p.calls, p.compactCalls, p.maxReq, window, AutoCompactThreshold(window))
	t.Logf("turn | heapKB | +goroutines | l.Messages | visibleTok | retainedUserCtxTok | compactions | prunes | provider calls")
	for _, s := range samples {
		t.Logf("%4d | %6d | %3d | %5d | %6d | %6d | %3d | %4d | %5d", s.turn, s.heapKB, s.goroutines, s.messages, s.visibleTokens, s.retained, s.compactions, s.prunes, s.rpc)
	}
	for _, e := range compactEvents {
		t.Logf("compaction: reason=%s removed=%d estimated=%d raw=%d threshold=%d(%s) window=%d", e.Reason, e.Removed, e.Estimated, e.RawEstimated, e.Threshold, e.ThresholdSource, e.Window)
		if e.Reason == "auto" && e.Estimated <= e.Threshold {
			t.Errorf("auto compaction below its threshold: %+v", e)
		}
		if e.Threshold > AutoCompactThreshold(window) {
			t.Errorf("threshold %d above the hard threshold %d", e.Threshold, AutoCompactThreshold(window))
		}
	}
	for _, v := range p.violations {
		t.Errorf("invariant: %s", v)
	}
	if p.pinMissing > 0 {
		t.Errorf("pinned turn-1 requirement missing from %d of %d requests", p.pinMissing, p.calls)
	}
	if want := 3 * turns; p.calls != want {
		t.Errorf("provider calls = %d, want %d (2 tool steps + answer per turn)", p.calls, want)
	}
	if compactions == 0 && prunes == 0 {
		t.Errorf("history grew past the window without prune/compaction")
	}
	last, first := samples[len(samples)-1], samples[0]
	if last.goroutines-first.goroutines > 10 {
		t.Errorf("goroutines grew by %d over the session", last.goroutines-first.goroutines)
	}
	if last.visibleTokens > window {
		t.Errorf("visible history %d tokens exceeds window %d", last.visibleTokens, window)
	}
	// History is bounded by compaction; the in-memory transcript must not
	// balloon (generous bound: 64 MiB of heap for a 200-turn session).
	if last.heapKB > 64<<10 {
		t.Errorf("heap %d KB after %d turns", last.heapKB, turns)
	}
}

func TestA130_LongSession200Turns(t *testing.T) {
	a130LongSession(t, 200)
}

func TestA130_SoakLongSession1000Turns(t *testing.T) {
	if os.Getenv("SUPERCLI_AUDIT_SOAK") != "1" {
		t.Skip("audit-130 soak test; set SUPERCLI_AUDIT_SOAK=1")
	}
	a130LongSession(t, 1000)
}

// a130RetainedTokens estimates the verbatim user-request carry that every
// compaction summary re-embeds (compact_user_context.go).
func a130RetainedTokens(msgs []llm.Message) int {
	for _, m := range msgs {
		if i := strings.Index(m.Content, "<retained-user-context-v1>"); i >= 0 {
			return llm.EstimateMessageTokens(llm.Message{Role: llm.RoleUser, Content: m.Content[i:]})
		}
	}
	return 0
}

// Long sessions with realistic user prompts (pasted logs/specs, ~2.5k
// tokens each). Compaction used to carry every earlier user request verbatim
// and unclipped, so the carry grew monotonically until compaction could no
// longer reduce the prefix and every following turn failed. The carry is now
// bounded (compact_user_context_budget.go); the session must keep running,
// compaction must actually happen, the carry must stay within its budget and
// the pinned turn-1 requirement must survive. docs/architecture.md:
// "compaction can never wedge the session".
func TestA130_LongSessionRetainedUserContextDoesNotWedge(t *testing.T) {
	const window = 32000
	p := &a130SessionProvider{window: window}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "probe", Description: "probe", Schema: `{"type":"object","properties":{"q":{"type":"string"}}}`,
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: strings.Repeat("probe output line\n", 40)}, nil
		}})
	reg.MarkAlwaysOn("probe")
	l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, System: a130PinSystem, MaxSteps: 10,
		WindowFor: func(string) int { return window }, Summarizer: NewAutoSummarizer(nil)})
	if err != nil {
		t.Fatal(err)
	}
	paste := strings.Repeat("2026-11-16T10:00:00Z worker[42] WARN cache miss key=user/profile/0042 latency=118ms\n", 110) // ~2.5k tokens
	failedAt, compactions, failedTurns, turns, maxRetained := 0, 0, 0, 0, 0
	var failure error
	var trail []string
	for turn := 1; turn <= 40 && (failedAt == 0 || turn <= failedAt+5); turn++ {
		turns = turn
		prompt := fmt.Sprintf("Please handle turn %d: run probe twice, then answer. Log excerpt:\n%s", turn, paste)
		if turn == 1 {
			prompt += " " + a130PinRequirement
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		ch, err := l.Run(ctx, prompt)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		var compacted []AutoCompactEvent
		for ev := range ch {
			switch e := ev.(type) {
			case ErrorEvent:
				failedTurns++
				if failedAt == 0 {
					failedAt, failure = turn, e.Err
				}
			case AutoCompactEvent:
				compactions++
				compacted = append(compacted, e)
			}
		}
		cancel()
		// l.Messages belongs to the run goroutine until its channel closes;
		// reading it while the turn runs raced (-race flake). The carry is
		// measured once the turn has ended.
		if len(compacted) > 0 {
			retained := a130RetainedTokens(l.Messages)
			maxRetained = max(maxRetained, retained)
			for _, e := range compacted {
				trail = append(trail, fmt.Sprintf("t%d: removed=%d est=%d retained=%dtok", turn, e.Removed, e.Estimated, retained))
			}
		}
	}
	t.Logf("compactions=%d trail=%v", compactions, trail)
	t.Logf("first failure at turn %d: %v; %d of %d turns failed", failedAt, failure, failedTurns, turns)
	if failedAt > 0 {
		t.Fatalf("session wedged at turn %d (%d of %d turns failed): %v", failedAt, failedTurns, turns, failure)
	}
	if compactions == 0 {
		t.Fatal("40 turns of ~2.5k-token prompts in a 32k window never compacted; the scenario no longer exercises the carry")
	}
	// Generous slack for the JSON envelope, header sentence and message framing.
	if budget := compactUserContextBudget(window); maxRetained > budget+200 {
		t.Errorf("retained user context %d tokens exceeds its budget %d", maxRetained, budget)
	}
	for _, v := range p.violations {
		t.Errorf("invariant: %s", v)
	}
	if p.pinMissing > 0 {
		t.Errorf("pinned turn-1 requirement missing from %d of %d requests", p.pinMissing, p.calls)
	}
}
