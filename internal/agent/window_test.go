package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestIsContextLimitErr(t *testing.T) {
	cases := map[string]bool{
		"This model's maximum context length is 8192 tokens.": true,
		"error code context_length_exceeded":                  true,
		"request failed: too many tokens":                     true,
		"connection refused":                                  false,
	}
	for msg, want := range cases {
		if got := isContextLimitErr(errors.New(msg)); got != want {
			t.Errorf("isContextLimitErr(%q) = %v, want %v", msg, got, want)
		}
	}
	if isContextLimitErr(nil) {
		t.Error("nil error must not match")
	}
}

func TestExtractContextLimit(t *testing.T) {
	cases := map[string]int{
		"This model's maximum context length is 8192 tokens": 8192,
		"model loaded with context length of only 4096":      4096,
		"too many tokens": 0,
	}
	for msg, want := range cases {
		if got := extractContextLimit(msg); got != want {
			t.Errorf("extractContextLimit(%q) = %d, want %d", msg, got, want)
		}
	}
}

func TestMaybeAutoCompact(t *testing.T) {
	echo, _ := llm.NewEcho("test")
	l := &Loop{
		provider:  echo,
		modelID:   "test",
		windowFor: func(string) int { return 100 }, // tiny window
		summarizer: func(ctx context.Context, p llm.Provider, msgs []llm.Message) (string, error) {
			return "SUMMARY", nil
		},
	}
	l.Messages = []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: strings.Repeat("x", 2000)}, // completed old turn
		{Role: llm.RoleAssistant, Content: "old answer"},
		{Role: llm.RoleUser, Content: "previous correction"},
		{Role: llm.RoleAssistant, Content: "previous answer"},
		{Role: llm.RoleUser, Content: "current task"},
	}
	out := make(chan Event, 4)
	l.maybeAutoCompact(context.Background(), out, "")
	if len(l.Messages) != 5 { // sys + summary + two newest user turns
		t.Fatalf("expected compaction to keep two recent turns, got %d messages", len(l.Messages))
	}
	if l.Messages[1].Content != "SUMMARY" {
		t.Errorf("summary message = %q", l.Messages[1].Content)
	}
	if l.Messages[2].Content != "previous correction" || l.Messages[4].Content != "current task" {
		t.Errorf("two recent turns were not preserved: %+v", l.Messages)
	}
	select {
	case ev := <-out:
		ac, ok := ev.(AutoCompactEvent)
		if !ok || ac.Removed != 2 || ac.Reason != "auto" {
			t.Errorf("unexpected event %+v", ev)
		}
	default:
		t.Error("expected AutoCompactEvent")
	}

	// Under threshold: no-op.
	before := len(l.Messages)
	l.maybeAutoCompact(context.Background(), out, "")
	if len(l.Messages) != before {
		t.Error("compacted below threshold")
	}
}

func TestEstimateNextRequestTokensIncludesToolSchemas(t *testing.T) {
	echo, _ := llm.NewEcho("test")
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "large_schema", Description: strings.Repeat("d", 900),
		Schema: `{"type":"object","properties":{"path":{"type":"string"}}}`,
		Fn:     func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil },
	})
	l := &Loop{provider: echo, registry: reg, route: RouteCoordinator}
	l.Messages = []llm.Message{{Role: llm.RoleUser, Content: "small message"}}
	if full, messages := l.EstimateNextRequestTokens(), l.EstimateVisibleTokens(); full <= messages {
		t.Fatalf("complete request estimate %d must exceed message-only estimate %d", full, messages)
	}
}

func TestResolveContextWindowSharedCascadeAndShortAlias(t *testing.T) {
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "openai/gpt-5.6-sol", ContextLength: 1_050_000})
	learned := llm.LoadLearnedLimits(t.TempDir())
	learned.Learn("learned-model", 65_536)

	const local = "http://localhost:1234/v1"

	if got := ResolveContextWindow("gpt-5.6-sol", 32_000, 64_000, caps, learned, local); got.Tokens != 32_000 || got.Source != "config" {
		t.Fatalf("config resolution = %+v", got)
	}
	if got := ResolveContextWindow("gpt-5.6-sol", 0, 128_000, caps, learned, local); got.Tokens != 128_000 || got.Source != "provider" {
		t.Fatalf("provider resolution = %+v", got)
	}
	if got := ResolveContextWindow("gpt-5.6-sol", 0, 0, caps, learned, local); got.Tokens != 1_050_000 || got.Source != "catalog-alias" {
		t.Fatalf("short-id catalog resolution = %+v", got)
	}
	caps.Register(llm.ModelInfo{ID: "other/gpt-5.6-sol", ContextLength: 512_000})
	if got := ResolveContextWindow("gpt-5.6-sol", 0, 0, caps, learned, local); got.Tokens != 0 {
		t.Fatalf("ambiguous short-id resolution = %+v, want unresolved", got)
	}
	if got := ResolveContextWindow("learned-model", 0, 0, caps, learned, local); got.Tokens != 65_536 || got.Source != "learned" {
		t.Fatalf("learned resolution = %+v", got)
	}
	if got := ResolveContextWindow("unknown", 0, 0, caps, learned, local); got.Tokens != 0 || got.Source != "" {
		t.Fatalf("unknown resolution = %+v, want unresolved", got)
	}
}

// TestResolveContextWindowUnknownRemoteModel pins the measured bug: a cloud
// gateway that publishes no context_length at all (the grok-4.5 reseller used
// for the live run) must not leave an unknown frontier model on the 16k
// self-hosted fallback, where the prune trigger sits at ~9.8k and one ordinary
// tool-heavy turn re-fires pruning every few calls.
func TestResolveContextWindowUnknownRemoteModel(t *testing.T) {
	caps := llm.NewCapabilityRegistry()
	learned := llm.LoadLearnedLimits(t.TempDir())

	got := ResolveContextWindow("grok-4.5", 0, 0, caps, learned, "https://x.example.invalid/v1")
	if got.Source != "fallback-remote" {
		t.Fatalf("unknown remote model resolved %+v, want the remote fallback", got)
	}
	if got.Tokens != defaultRemoteContextWindow {
		t.Fatalf("remote fallback = %d, want %d", got.Tokens, defaultRemoteContextWindow)
	}
	if got.Tokens <= defaultContextWindow {
		t.Fatalf("remote fallback %d must exceed the self-hosted fallback %d", got.Tokens, defaultContextWindow)
	}

	// Regression: a genuinely small self-hosted model stays protected. Local
	// backends do report their n_ctx, and when they do not, an overflow there
	// is not reliably reported as a recognizable context-length error — so the
	// conservative fallback must survive for them.
	for _, url := range []string{
		"http://localhost:1234/v1",
		"http://127.0.0.1:8080/v1",
		"http://192.168.0.103:8087/v1",
		"", // no endpoint known at all
	} {
		if got := ResolveContextWindow("some-local.gguf", 0, 0, caps, learned, url); got.Tokens != 0 {
			t.Fatalf("local endpoint %q resolved %+v, want unresolved (loop applies %d)", url, got, defaultContextWindow)
		}
	}

	// A real source still wins over the remote fallback in every direction,
	// including a small learned limit recovered from a provider overflow.
	learned.Learn("tiny-hosted", 8192)
	if got := ResolveContextWindow("tiny-hosted", 0, 0, caps, learned, "https://x.example.invalid/v1"); got.Tokens != 8192 || got.Source != "learned" {
		t.Fatalf("learned limit lost to the remote fallback: %+v", got)
	}
	caps.Register(llm.ModelInfo{ID: "small-hosted", ContextLength: 4096})
	if got := ResolveContextWindow("small-hosted", 0, 0, caps, learned, "https://x.example.invalid/v1"); got.Tokens != 4096 || got.Source != "catalog" {
		t.Fatalf("catalog entry lost to the remote fallback: %+v", got)
	}
}

func TestAutoCompactThresholdUsesBoundedReserve(t *testing.T) {
	cases := map[int]int{
		100:       88,
		16_384:    14_336,
		100_000:   90_000,
		1_050_000: 984_464,
	}
	for window, want := range cases {
		if got := autoCompactThreshold(window); got != want {
			t.Errorf("autoCompactThreshold(%d) = %d, want %d", window, got, want)
		}
	}
}

func TestNextRequestEstimateUsesExactBasePlusAppendOnlyDelta(t *testing.T) {
	echo, _ := llm.NewEcho("test")
	l := &Loop{provider: echo, route: RouteCoordinator}
	l.Messages = []llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("base ", 300)}}
	baseRaw := l.estimateNextRequestTokensRaw()
	l.recordContextBaseline(baseRaw, baseRaw-120)
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleTool, Content: strings.Repeat("new-result ", 60)})

	got := l.nextRequestTokenEstimate()
	want := (baseRaw - 120) + got.Raw - baseRaw
	if got.Source != "provider+delta" || got.Effective != want || got.ExactBase != baseRaw-120 {
		t.Fatalf("hybrid estimate = %+v, want effective=%d", got, want)
	}

	// A context rewrite invalidates the append-only baseline. Falling back to
	// the raw estimate is safer than carrying an old calibration across it.
	l.Messages = []llm.Message{{Role: llm.RoleUser, Content: "compacted"}}
	shrunk := l.nextRequestTokenEstimate()
	if shrunk.Source != "estimate" || shrunk.Effective != shrunk.Raw {
		t.Fatalf("estimate after shrink = %+v, want raw fallback", shrunk)
	}
}

func TestNextRequestEstimateDoesNotReuseChatBaselineAfterRouteChange(t *testing.T) {
	echo, _ := llm.NewEcho("test")
	l := &Loop{provider: echo, route: RouteChatOnly}
	for i := 0; i < 40; i++ {
		l.Messages = append(l.Messages,
			llm.Message{Role: llm.RoleUser, Content: strings.Repeat("history ", 80)},
			llm.Message{Role: llm.RoleAssistant, Content: "answer"},
		)
	}
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "current"})
	chatRaw := l.estimateNextRequestTokensRaw()
	l.recordContextBaseline(chatRaw, chatRaw)

	l.route = RouteCoordinator
	coordinatorRaw := l.estimateNextRequestTokensRaw()
	if coordinatorRaw <= chatRaw {
		t.Fatalf("test setup: coordinator=%d must exceed chat=%d", coordinatorRaw, chatRaw)
	}
	got := l.nextRequestTokenEstimate()
	if got.Source != "estimate" || got.Effective != coordinatorRaw {
		t.Fatalf("route-changed estimate = %+v, want fresh raw %d", got, coordinatorRaw)
	}
}

func TestScopedContextWindowKeepsSameModelProvidersSeparate(t *testing.T) {
	l := &Loop{
		modelID: "gpt-5.6-sol", contextProvider: "anyrouter",
		scopedWindowFor: func(provider, model string) ContextWindowResolution {
			if provider == "anyrouter" && model == "gpt-5.6-sol" {
				return ContextWindowResolution{Tokens: 100_000, Source: "model-override"}
			}
			return ContextWindowResolution{}
		},
		contextWindowFor: func(string) ContextWindowResolution {
			return ContextWindowResolution{Tokens: 1_050_000, Source: "catalog-alias"}
		},
	}
	if got := l.windowResolution(); got.Tokens != 100_000 || got.Source != "model-override" {
		t.Fatalf("AnyRouter resolution = %+v", got)
	}
	l.SetContextProvider("openai")
	if got := l.windowResolution(); got.Tokens != 1_050_000 || got.Source != "catalog-alias" {
		t.Fatalf("OpenAI resolution = %+v", got)
	}
}

func TestCompactNowKeepsProjectionUnchangedOnSummaryFailure(t *testing.T) {
	echo, _ := llm.NewEcho("test")
	l := &Loop{
		provider:  echo,
		windowFor: func(string) int { return 2000 },
		summarizer: func(context.Context, llm.Provider, []llm.Message) (string, error) {
			return "", errors.New("backend unavailable")
		},
	}
	l.Messages = []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "old"},
		{Role: llm.RoleAssistant, Content: "answer"},
	}
	before := append([]llm.Message(nil), l.Messages...)
	if _, err := l.CompactNow(context.Background()); err == nil {
		t.Fatal("CompactNow error = nil, want summary failure")
	}
	if !reflect.DeepEqual(l.Messages, before) {
		t.Fatalf("messages changed after failed manual compaction: %#v", l.Messages)
	}
}

func TestCompactNowRejectsSummaryThatDoesNotReduceContext(t *testing.T) {
	echo, _ := llm.NewEcho("test")
	l := &Loop{
		provider:  echo,
		windowFor: func(string) int { return 2000 },
		summarizer: func(_ context.Context, _ llm.Provider, msgs []llm.Message) (string, error) {
			return strings.Repeat("verbose summary ", 1000), nil
		},
	}
	l.Messages = []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: strings.Repeat("old context ", 100)},
		{Role: llm.RoleAssistant, Content: "answer"},
		{Role: llm.RoleUser, Content: "recent turn kept verbatim"},
	}
	before := append([]llm.Message(nil), l.Messages...)
	if _, err := l.CompactNow(context.Background()); err == nil || !strings.Contains(err.Error(), "insufficient reduction") {
		t.Fatalf("CompactNow error = %v, want insufficient reduction", err)
	}
	if !reflect.DeepEqual(l.Messages, before) {
		t.Fatalf("messages changed after ineffective manual compaction: %#v", l.Messages)
	}
}

func TestMaybeAutoCompactDoesNotResummarizeSummaryOnlyPrefix(t *testing.T) {
	echo, _ := llm.NewEcho("test")
	calls := 0
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "fixed_overhead", Description: strings.Repeat("schema", 700), Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil },
	})
	l := &Loop{
		provider: echo, registry: reg, route: RouteCoordinator,
		windowFor: func(string) int { return 1000 },
		summarizer: func(context.Context, llm.Provider, []llm.Message) (string, error) {
			calls++
			return "unexpected", nil
		},
	}
	l.Messages = []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "This session is continued from a previous conversation that was compacted to save context."},
		{Role: llm.RoleUser, Content: "current"},
	}
	l.maybeAutoCompact(context.Background(), nil, "")
	if calls != 0 {
		t.Fatalf("summary-only prefix was summarized %d time(s)", calls)
	}
}

func TestMaybeAutoCompactDoesNotSummarizeLargeActiveTurn(t *testing.T) {
	echo, _ := llm.NewEcho("test")
	calls := 0
	l := &Loop{
		provider: echo, modelID: "test",
		windowFor: func(string) int { return 2000 },
		summarizer: func(context.Context, llm.Provider, []llm.Message) (string, error) {
			calls++
			return "unexpected", nil
		},
	}
	summary := "This session is continued from a previous conversation that was compacted to save context."
	l.Messages = []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: summary},
		{Role: llm.RoleUser, Content: "current task"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "read", Name: "read_lines", Arguments: `{"file":"large.go"}`},
		}},
		{Role: llm.RoleTool, ToolCallID: "read", Name: "read_lines", Content: strings.Repeat("large result ", 1000)},
	}
	before := append([]llm.Message(nil), l.Messages...)

	l.maybeAutoCompact(context.Background(), nil, "")

	if calls != 0 {
		t.Fatalf("active turn was summarized %d time(s)", calls)
	}
	if !reflect.DeepEqual(l.Messages, before) {
		t.Fatalf("active turn changed during speculative compaction: %#v", l.Messages)
	}
}

func TestAutoCompactSplitPreservesTwoRecentUserTurns(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "oldest"},
		{Role: llm.RoleAssistant, Content: "done"},
		{Role: llm.RoleUser, Content: "previous"},
		{Role: llm.RoleAssistant, Content: "also done"},
		{Role: llm.RoleUser, Content: "current"},
		{Role: llm.RoleTool, Content: strings.Repeat("x", 10000)},
	}
	if got := autoCompactSplit(msgs); got != 3 {
		t.Fatalf("autoCompactSplit = %d, want previous user index 3", got)
	}
	singleTurn := msgs[:3]
	if got := autoCompactSplit(singleTurn); got != 1 {
		t.Fatalf("single-turn autoCompactSplit = %d, want leading boundary/current user 1", got)
	}
}

// TestMaybeAutoCompact_KeepsLastTurn: when the bulk sits in OLDER
// turns, the summary replaces only those; the last user turn (the
// work in progress) survives verbatim after the summary — and the
// summarizer never even sees it.
func TestMaybeAutoCompact_KeepsLastTurn(t *testing.T) {
	echo, _ := llm.NewEcho("test")
	var summarized []llm.Message
	l := &Loop{
		provider:  echo,
		modelID:   "test",
		windowFor: func(string) int { return 2000 },
		summarizer: func(ctx context.Context, p llm.Provider, msgs []llm.Message) (string, error) {
			summarized = append([]llm.Message(nil), msgs...)
			return "SUMMARY", nil
		},
	}
	l.Messages = []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: strings.Repeat("x", 6000)}, // old bulk
		{Role: llm.RoleAssistant, Content: "done earlier"},
		{Role: llm.RoleUser, Content: "previous correction"},
		{Role: llm.RoleAssistant, Content: "acknowledged"},
		{Role: llm.RoleUser, Content: "current question"},
		{Role: llm.RoleAssistant, Content: "working on it"},
	}
	l.maybeAutoCompact(context.Background(), nil, "")

	want := []string{"sys", "SUMMARY", "previous correction", "acknowledged", "current question", "working on it"}
	if len(l.Messages) != len(want) {
		t.Fatalf("Messages = %d entries, want %d: %+v", len(l.Messages), len(want), l.Messages)
	}
	for i, w := range want {
		if l.Messages[i].Content != w {
			t.Errorf("Messages[%d] = %q, want %q", i, l.Messages[i].Content, w)
		}
	}
	for _, m := range summarized {
		if m.Content == "previous correction" || m.Content == "acknowledged" || m.Content == "current question" || m.Content == "working on it" {
			t.Errorf("last turn leaked into the summarizer input: %q", m.Content)
		}
	}
}

// TestMaybeAutoCompact_HugeLastTurnFallsBackToFull: when the last
// turn alone still eats more than half the window, cutting at the
// turn boundary would leave the context over budget — everything is
// summarized (the historical behaviour, exercised above in
// TestMaybeAutoCompact with a single huge user message).
func TestMaybeAutoCompact_HugeLastTurnFallsBackToFull(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "small old turn"},
		{Role: llm.RoleAssistant, Content: "ok"},
		{Role: llm.RoleUser, Content: strings.Repeat("y", 6000)}, // huge current turn
	}
	if got := compactSplit(msgs, 2000); got != len(msgs) {
		t.Errorf("compactSplit = %d, want %d (full compaction)", got, len(msgs))
	}
}
