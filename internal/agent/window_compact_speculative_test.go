package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func speculativeHistory() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleUser, Content: "OLD-REQUEST " + strings.Repeat("old evidence ", 400)},
		{Role: llm.RoleAssistant, Content: "OLD-DONE " + strings.Repeat("report ", 400)},
		{Role: llm.RoleUser, Content: "previous correction"},
		{Role: llm.RoleAssistant, Content: "ack"},
		{Role: llm.RoleUser, Content: "current task"},
	}
}

func newSpeculativeLoop(t *testing.T, summarize Summarizer) *Loop {
	t.Helper()
	l, err := NewLoop(LoopConfig{
		Provider: &stubProvider{name: "fixture"}, Registry: tools.NewRegistry(),
		InitialMessages: speculativeHistory(), MaxSteps: 3, PruneProtectTokens: -1,
		Summarizer: summarize, TaskParallel: true,
		WindowFor: func(string) int { return 1_000_000 },
	})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func speculativePrefix(l *Loop) []llm.Message {
	history := l.compactionHistory()
	return history.messages[:history.autoCompactSplit()]
}

func TestSpeculativeCompactFreshResultUsed(t *testing.T) {
	var calls atomic.Int32
	l := newSpeculativeLoop(t, func(_ context.Context, _ llm.Provider, msgs []llm.Message) (string, error) {
		calls.Add(1)
		return WrapCompactSummary("Goal: old. Pending: none."), nil
	})
	ctx := context.Background()
	l.maybeStartSpeculativeCompact(ctx)
	l.maybeStartSpeculativeCompact(ctx) // same prefix: no second call
	summary, err, _, ok := l.takeSpeculativeCompact(ctx, speculativePrefix(l), true)
	if !ok || err != nil || !strings.Contains(summary, "Goal: old") || calls.Load() != 1 {
		t.Fatalf("fresh result: ok=%v err=%v calls=%d summary=%q", ok, err, calls.Load(), summary)
	}
	if l.speculative != nil {
		t.Fatal("consumed job retained")
	}
}

func TestSpeculativeCompactStalePrefixDiscarded(t *testing.T) {
	for _, change := range []string{"edit", "epoch"} {
		t.Run(change, func(t *testing.T) {
			var calls atomic.Int32
			l := newSpeculativeLoop(t, func(context.Context, llm.Provider, []llm.Message) (string, error) {
				calls.Add(1)
				return WrapCompactSummary("Goal: stale."), nil
			})
			l.maybeStartSpeculativeCompact(context.Background())
			if change == "edit" {
				l.Messages[1].Content = "pruned in place"
			} else {
				l.conversationEpoch.Add(1)
			}
			if _, _, _, ok := l.takeSpeculativeCompact(context.Background(), speculativePrefix(l), true); ok {
				t.Fatal("stale speculative summary was used")
			}
			if calls.Load() != 1 || l.speculative != nil {
				t.Fatalf("stale job: calls=%d retained=%v", calls.Load(), l.speculative != nil)
			}
		})
	}
}

func TestSpeculativeCompactCancellation(t *testing.T) {
	started := make(chan struct{})
	exited := make(chan struct{})
	l := newSpeculativeLoop(t, func(ctx context.Context, _ llm.Provider, _ []llm.Message) (string, error) {
		defer close(exited)
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})
	runCtx, cancel := context.WithCancel(context.Background())
	l.maybeStartSpeculativeCompact(runCtx)
	<-started
	waitCtx, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if _, err, _, ok := l.takeSpeculativeCompact(waitCtx, speculativePrefix(l), true); !ok || err == nil {
		t.Fatalf("canceled wait: ok=%v err=%v", ok, err)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("background summary leaked after cancellation")
	}
	cancel()
	// Run-context cancellation alone also stops a job.
	started, exited = make(chan struct{}), make(chan struct{})
	runCtx, cancel = context.WithCancel(context.Background())
	l.maybeStartSpeculativeCompact(runCtx)
	<-started
	cancel()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("background summary ignored run cancellation")
	}
	l.stopSpeculativeCompact()
}

func TestSpeculativeCompactSingleFlightAndGates(t *testing.T) {
	var running, peak atomic.Int32
	release := make(chan struct{})
	l := newSpeculativeLoop(t, func(ctx context.Context, _ llm.Provider, _ []llm.Message) (string, error) {
		n := running.Add(1)
		defer running.Add(-1)
		if n > peak.Load() {
			peak.Store(n)
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return WrapCompactSummary("Goal: x."), nil
	})
	l.maybeStartSpeculativeCompact(context.Background())
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleAssistant, Content: "done"}, llm.Message{Role: llm.RoleUser, Content: "next"})
	l.maybeStartSpeculativeCompact(context.Background()) // grown prefix: retain the original job
	close(release)
	l.stopSpeculativeCompact()
	if peak.Load() != 1 {
		t.Fatalf("concurrent background summaries: %d", peak.Load())
	}
	// Local single-slot backends and short histories never speculate.
	var calls atomic.Int32
	local := newSpeculativeLoop(t, func(context.Context, llm.Provider, []llm.Message) (string, error) {
		calls.Add(1)
		return "", nil
	})
	local.taskParallel = false
	local.maybeStartSpeculativeCompact(context.Background())
	short := newSpeculativeLoop(t, func(context.Context, llm.Provider, []llm.Message) (string, error) {
		calls.Add(1)
		return "", nil
	})
	short.Messages = short.Messages[len(short.Messages)-1:]
	short.maybeStartSpeculativeCompact(context.Background())
	if calls.Load() != 0 || local.speculative != nil || short.speculative != nil {
		t.Fatalf("speculation without need: calls=%d", calls.Load())
	}
}

// The synchronous threshold path consumes a matching background result
// instead of making its own blocking summary call.
func TestSpeculativeCompactUsedByAutoCompact(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	window := 1_000_000
	l := newSpeculativeLoop(t, func(context.Context, llm.Provider, []llm.Message) (string, error) {
		calls.Add(1)
		<-release
		return WrapCompactSummary("Goal: old. Pending: continue."), nil
	})
	l.windowFor = func(string) int { return window }
	l.contextModel = contextModelState{loaded: true, provider: l.prefillScope(), model: l.modelID}
	est := l.nextRequestTokenEstimate().Effective
	window = est + est/10 + minContextReserve // threshold just above the estimate
	if th := autoCompactThreshold(window); est > th || float64(est) < speculativeCompactFrac*float64(th) {
		t.Fatalf("fixture outside speculative band: est=%d threshold=%d", est, th)
	}
	if err := l.maybeAutoCompact(context.Background(), nil, ""); err != nil {
		t.Fatal(err)
	}
	if l.speculative == nil {
		t.Fatal("no background summary started in the speculative band")
	}
	close(release)
	<-l.speculative.done
	window = 100 // now over threshold
	if err := l.maybeAutoCompact(context.Background(), nil, ""); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || !IsLegacyCompactionSummary(l.Messages[0]) {
		t.Fatalf("threshold path: calls=%d first=%q", calls.Load(), l.Messages[0].Content)
	}
}

func TestCompactSummaryInputCapsOlderTurnsOnly(t *testing.T) {
	long := "HEAD " + strings.Repeat("ż", 5000) + " TAIL"
	tool := llm.Message{Role: llm.RoleTool, ToolCallID: "r", Content: strings.Repeat("tool ", 1000)}
	summary := llm.Message{Role: llm.RoleUser, Content: WrapCompactSummary(strings.Repeat("fact ", 900))}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		summary,
		{Role: llm.RoleUser, Content: long},
		{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: long}}, ToolCalls: []llm.ToolCall{{ID: "r", Name: "read", Arguments: `{"a":1}`}}},
		tool,
		{Role: llm.RoleUser, Content: "recent " + long},
		{Role: llm.RoleAssistant, Content: "recent reply " + long},
		{Role: llm.RoleUser, Content: "latest " + long},
	}
	got := compactSummaryInput(msgs)
	full := RenderCompactTranscript(msgs)
	if len(got) >= len(full) || strings.Count(got, " chars omitted ...]") != 2 {
		t.Fatalf("older texts not capped: got=%d full=%d", len(got), len(full))
	}
	if !strings.Contains(got, "[... 3506 chars omitted ...]") || !strings.Contains(got, "HEAD ") || !strings.Contains(got, " TAIL\n[tool call: read {\"a\":1}]") {
		t.Fatalf("head/tail/marker/tool call lost: %q", got[:min(len(got), 400)])
	}
	for _, kept := range []string{"recent " + long, "recent reply " + long, "latest " + long, compactTranscriptContent(summary), compactTranscriptContent(tool)} {
		if !strings.Contains(got, kept) {
			t.Fatal("recent turn, prior summary or tool excerpt changed")
		}
	}
	if !strings.Contains(full, "[user] "+long) || msgs[2].Content != long || msgs[3].Parts[0].Text != long {
		t.Fatal("full render or canonical history changed")
	}
	for _, line := range strings.Split(got, "\n") {
		if !utf8ValidString(line) {
			t.Fatal("cap split a UTF-8 rune")
		}
	}
}

func utf8ValidString(s string) bool { return strings.ToValidUTF8(s, "\uFFFD") == s }
