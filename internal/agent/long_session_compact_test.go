package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestLongSessionCompactRetiresFinishedStaleJob(t *testing.T) {
	for _, change := range []string{"tool activation", "history edit"} {
		t.Run(change, func(t *testing.T) {
			var calls atomic.Int32
			l := newSpeculativeLoop(t, func(context.Context, llm.Provider, []llm.Message) (string, error) {
				calls.Add(1)
				return WrapCompactSummary("Goal: old request. Done: verified report. State: preserve restrictions."), nil
			})
			l.registry.MustRegister(tools.Tool{Name: "late", Description: "Additional discovered tool.", Schema: `{"type":"object"}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil }})
			defer l.stopSpeculativeCompact()
			l.maybeStartSpeculativeCompact(context.Background())
			first := l.speculative
			waitCompactTest(t, first.done)
			if change == "tool activation" {
				l.registry.Activate("late")
			} else {
				l.Messages[1].Content += " Updated observation."
			}
			l.maybeStartSpeculativeCompact(context.Background())
			if l.speculative == nil || l.speculative == first {
				t.Fatal("completed stale job blocked fresh compaction")
			}
			waitCompactTest(t, l.speculative.done)
			_, err, _, ok := l.takeSpeculativeCompact(context.Background(), speculativePrefix(l), false)
			if !ok || err != nil || calls.Load() != 2 {
				t.Fatalf("fresh result unavailable: accepted=%v err=%v calls=%d", ok, err, calls.Load())
			}
		})
	}
}

func TestLongSessionCompactNonblockingTakeReleasesStaleJob(t *testing.T) {
	l := newSpeculativeLoop(t, func(context.Context, llm.Provider, []llm.Message) (string, error) {
		return WrapCompactSummary("Done: old observation."), nil
	})
	defer l.stopSpeculativeCompact()
	l.maybeStartSpeculativeCompact(context.Background())
	waitCompactTest(t, l.speculative.done)
	l.Messages[1].Content += " Changed after capture."
	if _, _, _, ok := l.takeSpeculativeCompact(context.Background(), speculativePrefix(l), false); ok || l.speculative != nil {
		t.Fatal("nonblocking consumption retained or accepted stale summary")
	}
}

func TestLongSessionCompactConsumesReadyJobBelowStartBand(t *testing.T) {
	var calls atomic.Int32
	l := newSpeculativeLoop(t, func(context.Context, llm.Provider, []llm.Message) (string, error) {
		calls.Add(1)
		return WrapCompactSummary("Goal: old request. Done: old report. State: preserve restrictions."), nil
	})
	defer l.stopSpeculativeCompact()
	l.maybeStartSpeculativeCompact(context.Background())
	waitCompactTest(t, l.speculative.done)
	// Starting new background work needs pressure; consuming already completed,
	// applicable work must not depend on remaining in that same start band.
	before := llm.EstimateTokens(l.Messages)
	suffix := cloneCompactPrefix(l.Messages[2:])
	if err := l.maybeAutoCompact(context.Background(), nil, ""); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || l.speculative != nil || !strings.HasPrefix(l.Messages[0].Content, compactSummaryPreamble) {
		t.Fatal("ready useful summary stranded below the speculation start band")
	}
	if llm.EstimateTokens(l.Messages) >= before || len(l.Messages) != len(suffix)+1 {
		t.Fatal("completed summary did not reduce old context")
	}
	for i := range suffix {
		a, _ := json.Marshal(suffix[i])
		b, _ := json.Marshal(l.Messages[i+1])
		if string(a) != string(b) {
			t.Fatal("recent instruction or active state changed")
		}
	}
}
