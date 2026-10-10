package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
)

func TestSummarizeRejectsEmptyInstructionTemplate(t *testing.T) {
	for _, text := range []string{
		compactionTemplate,
		"\n \t" + strings.ReplaceAll(compactionTemplate, "\n", "\r\n\n \t") + "\n",
		compactionPrompt,
		"<thinking>private reasoning</thinking>\n" + compactionTemplate,
	} {
		p := &stubProvider{name: "summary", scripts: [][]llm.Delta{{
			{Content: text}, {FinishReason: "stop"}, {Usage: &llm.Usage{Input: 10, Output: 100}},
		}}}
		got, err := SummarizeForCompaction(context.Background(), p, []llm.Message{{Role: llm.RoleUser, Content: "Fix parser; never publish."}})
		if got != "" || !errors.Is(err, errCompactionInstructionEcho) || atomic.LoadInt32(&p.calls) != 1 {
			t.Fatalf("empty instruction echo accepted/retried: got=%q err=%v calls=%d", got, err, p.calls)
		}
	}
}

func TestSummarizeInstructionEchoGuardKeepsMeaningfulText(t *testing.T) {
	for _, text := range []string{
		"Goal: fix parser\nDone: one patch verified\nState: never publish\nPending: documentation",
		"Goal: fix parser\nDone: tests passed", // Do not impose a new format requirement.
		compactionTemplate + "\nActual facts: tests passed; no publishing; docs pending.",
		"Goal: implement this exact template:\n" + compactionTemplate + "\nDone: template added\nState: no dependencies\nPending: review",
	} {
		p := &stubProvider{name: "summary", scripts: [][]llm.Delta{{{Content: text}, {FinishReason: "stop"}}}}
		got, err := SummarizeForCompaction(context.Background(), p, []llm.Message{{Role: llm.RoleUser, Content: "older task"}})
		if err != nil || got != text || atomic.LoadInt32(&p.calls) != 1 {
			t.Fatalf("meaningful summary rejected/changed: got=%q err=%v", got, err)
		}
	}
}

func echoSummaryHistory() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleSystem, Content: "standing policy"},
		{Role: llm.RoleUser, Content: "Fix parser; never publish; retain the existing API."},
		{Role: llm.RoleAssistant, Content: strings.Repeat("verified evidence ", 2000)},
		{Role: llm.RoleUser, Content: "previous correction"},
		{Role: llm.RoleAssistant, Content: "acknowledged"},
		{Role: llm.RoleUser, Content: "current task"},
	}
}

func TestInstructionEchoPreservesManualAutomaticAndEmergencyHistory(t *testing.T) {
	for _, mode := range []string{"manual", "automatic", "context-limit"} {
		t.Run(mode, func(t *testing.T) {
			p := &stubProvider{name: "summary", scripts: [][]llm.Delta{{{Content: compactionTemplate}, {FinishReason: "stop"}}}}
			history := echoSummaryHistory()
			l := &Loop{provider: p, windowFor: func(string) int { return 100 }, summarizer: NewAutoSummarizer(nil), Messages: append([]llm.Message(nil), history...)}
			before := l.VisibleMessages()
			out := make(chan Event, 4)
			var err error
			if mode == "manual" {
				var ev AutoCompactEvent
				ev, err = l.CompactNow(context.Background())
				if ev.Removed != 0 {
					t.Fatalf("invalid summary removed %d messages", ev.Removed)
				}
			} else {
				reason := ""
				if mode == "context-limit" {
					reason = mode
				}
				err = l.maybeAutoCompact(context.Background(), out, reason)
			}
			if !errors.Is(err, errCompactionInstructionEcho) || !reflect.DeepEqual(l.Messages, history) || !reflect.DeepEqual(l.VisibleMessages(), before) || len(out) != 0 || atomic.LoadInt32(&p.calls) != 1 {
				t.Fatalf("invalid summary changed/hid history or retried: err=%v events=%d calls=%d", err, len(out), p.calls)
			}
		})
	}
}

func TestInstructionEchoStopsRunBeforeFurtherInference(t *testing.T) {
	history := echoSummaryHistory()
	p := &stubProvider{name: "summary", scripts: [][]llm.Delta{
		{{Content: compactionTemplate}, {FinishReason: "stop"}},
		{{Content: "main must not run without prior facts"}, {FinishReason: "stop"}},
	}}
	l, err := NewLoop(LoopConfig{Provider: p, Registry: echoToolRegistry(t), MaxSteps: 3, WindowFor: func(string) int { return 100 }, Summarizer: NewAutoSummarizer(nil), PruneProtectTokens: -1, InitialMessages: history[:len(history)-1]})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise threshold compaction on a warm model. Model-switch compaction
	// intentionally retains full history and may continue after helper failure.
	l.contextModel = contextModelState{loaded: true, provider: l.prefillScope(), model: l.modelID}
	ch, err := l.Run(context.Background(), "current task")
	if err != nil {
		t.Fatal(err)
	}
	errorsSeen, done := 0, false
	for event := range ch {
		switch event := event.(type) {
		case ErrorEvent:
			if !errors.Is(event.Err, errCompactionInstructionEcho) {
				t.Fatalf("unexpected error: %v", event.Err)
			}
			errorsSeen++
		case DoneEvent:
			done = true
		}
	}
	if atomic.LoadInt32(&p.calls) != 1 || errorsSeen != 1 || done || !reflect.DeepEqual(l.Messages, history) {
		t.Fatalf("echo should abort once with intact history: calls=%d errors=%d done=%v tool requests=%v", p.calls, errorsSeen, done, p.toolReqs)
	}
}

func TestInstructionEchoStopsContextOverflowRetry(t *testing.T) {
	history := echoSummaryHistory()
	p := &stubProvider{name: "summary", scripts: [][]llm.Delta{
		{{Err: errors.New("context length is 4096 tokens: too many tokens")}},
		{{Content: compactionTemplate}, {FinishReason: "stop"}},
		{{Content: "main retry must not run without prior facts"}, {FinishReason: "stop"}},
	}}
	window := 50_000
	l, err := NewLoop(LoopConfig{Provider: p, Registry: echoToolRegistry(t), MaxSteps: 3, WindowFor: func(string) int { return window }, LearnLimit: func(_ string, limit int) { window = limit }, Summarizer: NewAutoSummarizer(nil), PruneProtectTokens: -1, InitialMessages: history[:len(history)-1]})
	if err != nil {
		t.Fatal(err)
	}
	l.contextModel = contextModelState{loaded: true, provider: l.prefillScope(), model: l.modelID}
	ch, err := l.Run(context.Background(), "current task")
	if err != nil {
		t.Fatal(err)
	}
	errorsSeen := 0
	for event := range ch {
		if event, ok := event.(ErrorEvent); ok {
			if !errors.Is(event.Err, errCompactionInstructionEcho) {
				t.Fatalf("unexpected error: %v", event.Err)
			}
			errorsSeen++
		}
	}
	if atomic.LoadInt32(&p.calls) != 2 || errorsSeen != 1 || !reflect.DeepEqual(l.Messages, history) || window != 4096 {
		t.Fatalf("overflow should not retry/hide after empty template: calls=%d errors=%d window=%d", p.calls, errorsSeen, window)
	}
}

func TestSideModelInstructionEchoSurvivesFailedFallback(t *testing.T) {
	side := &stubProvider{name: "side", scripts: [][]llm.Delta{{{Content: compactionTemplate}, {FinishReason: "stop"}}}}
	active := &failingSummaryProvider{}
	history := echoSummaryHistory()
	l := &Loop{provider: active, route: RouteCoordinator, windowFor: func(string) int { return 100 }, summarizer: NewAutoSummarizerWithProvider(side, nil), Messages: append([]llm.Message(nil), history...)}
	before := l.VisibleMessages()
	err := l.maybeAutoCompact(context.Background(), nil, "")
	if !errors.Is(err, errCompactionInstructionEcho) || !reflect.DeepEqual(l.Messages, history) || !reflect.DeepEqual(l.VisibleMessages(), before) || atomic.LoadInt32(&side.calls) != 1 || active.calls.Load() != 1 {
		t.Fatalf("side echo lost during fallback and allowed hiding/retries: err=%v side=%d active=%d", err, side.calls, active.calls.Load())
	}
}
