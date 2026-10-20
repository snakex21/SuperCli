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

func resolverSummaryFixture() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleSystem, Content: "system instruction is not part of helper history"},
		{Role: llm.RoleUser, Content: "Optimize all models. Keep my thinking level and earlier requirements."},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "edit1", Name: "write_file", Arguments: `{"path":"internal/agent/fixture.go","content":"package agent"}`}}},
		{Role: llm.RoleTool, Name: "write_file", ToolCallID: "edit1", Content: "Created internal/agent/fixture.go (13 bytes)"},
	}
}

func newResolverSummaryProvider(name string) *stubProvider {
	return &stubProvider{name: name, scripts: [][]llm.Delta{{
		{Content: "Goal: optimize all models\nDone: edited fixture.go\nState: keep thinking level and earlier requirements\nPending: validate"},
		{FinishReason: "stop"},
	}}}
}

func TestAutoSummarizerResolverPreservesRequestFactsAndToolScope(t *testing.T) {
	main := newResolverSummaryProvider("main")
	side := newResolverSummaryProvider("side")
	msgs := resolverSummaryFixture()
	before := append([]llm.Message(nil), msgs...)
	resolves := 0
	resolve := func(context.Context) (llm.Provider, error) { resolves++; return side, nil }
	summarize := NewAutoSummarizerWithProviderResolver(resolve, func() []string { return []string{"wrong_global_tool"} })
	if resolves != 0 {
		t.Fatal("summarizer construction resolved optional backend")
	}
	ctx := context.WithValue(context.Background(), compactToolScopeKey{}, compactToolScope{activeTools: func() []string { return []string{"write_file", "run"} }})
	got, err := summarize(ctx, main, msgs)
	if err != nil {
		t.Fatal(err)
	}
	wantRequest := []llm.Message{{Role: llm.RoleSystem, Content: compactionStatePrompt}, {Role: llm.RoleUser, Content: RenderCompactTranscript(msgs)}}
	if resolves != 1 || len(side.reqs) != 1 || !reflect.DeepEqual(side.reqs[0], wantRequest) || len(side.toolDefsReqs[0]) != 0 || len(main.reqs) != 0 {
		t.Fatalf("resolver changed selected provider, helper input or no-tools contract: resolves=%d side=%+v mainCalls=%d", resolves, side.reqs, len(main.reqs))
	}
	for _, fact := range []string{"keep thinking level and earlier requirements", "files_modified: internal/agent/fixture.go", "loaded_tools: write_file, run"} {
		if !strings.Contains(got, fact) {
			t.Errorf("summary lost %q: %s", fact, got)
		}
	}
	if strings.Contains(got, "wrong_global_tool") || !reflect.DeepEqual(msgs, before) {
		t.Fatal("summary leaked another loop's tool scope or mutated history")
	}
}

func TestAutoSummarizerResolverBuildFailureUsesOneMainCall(t *testing.T) {
	main := newResolverSummaryProvider("main")
	resolves := 0
	summarize := NewAutoSummarizerWithProviderResolver(func(context.Context) (llm.Provider, error) {
		resolves++
		return nil, errors.New("optional backend unavailable")
	}, nil)
	if _, err := summarize(context.Background(), main, resolverSummaryFixture()); err != nil {
		t.Fatal(err)
	}
	if resolves != 1 || atomic.LoadInt32(&main.calls) != 1 {
		t.Fatalf("build failure repeated provider calls: resolve=%d main=%d", resolves, main.calls)
	}
}

func TestAutoSummarizerResolverCancellationKeepsLoopHistory(t *testing.T) {
	main := newResolverSummaryProvider("main")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolves := 0
	summarize := NewAutoSummarizerWithProviderResolver(func(context.Context) (llm.Provider, error) {
		resolves++
		cancel()
		return nil, errors.New("canceled construction")
	}, nil)
	l := &Loop{provider: main, summarizer: summarize, windowFor: func(string) int { return 10_000 }}
	l.Messages = append(resolverSummaryFixture(),
		llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("completed old work ", 100)},
		llm.Message{Role: llm.RoleUser, Content: "recent instruction kept verbatim"},
		llm.Message{Role: llm.RoleAssistant, Content: "recent answer"},
		llm.Message{Role: llm.RoleUser, Content: "current request kept verbatim"},
	)
	before := append([]llm.Message(nil), l.Messages...)
	if _, err := l.CompactNow(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("compaction cancellation changed: %v", err)
	}
	if resolves != 1 || len(main.reqs) != 0 || !reflect.DeepEqual(l.Messages, before) {
		t.Fatal("cancelled resolver launched fallback or changed history")
	}
	if _, err := summarize(ctx, main, before); !errors.Is(err, context.Canceled) || resolves != 1 {
		t.Fatal("already-cancelled compaction resolved another backend")
	}
}

func TestAutoSummarizerResolverNotUsedBelowCompactThreshold(t *testing.T) {
	main := newResolverSummaryProvider("main")
	resolves := 0
	l := &Loop{provider: main, windowFor: func(string) int { return 100_000 },
		summarizer: NewAutoSummarizerWithProviderResolver(func(context.Context) (llm.Provider, error) { resolves++; return main, nil }, nil),
	}
	l.Messages = []llm.Message{{Role: llm.RoleSystem, Content: "system"}, {Role: llm.RoleUser, Content: "ordinary short request"}}
	l.maybeAutoCompact(context.Background(), nil, "")
	if resolves != 0 || len(main.reqs) != 0 {
		t.Fatal("ordinary short turn resolved or invoked compaction backend")
	}
}
