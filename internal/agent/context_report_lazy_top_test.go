package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// This oracle preserves the report implementation before lazy top-item labels.
// It deliberately uses the original full-text normalization and stable sort.
func legacyContextReportTopOracle(l *Loop) ContextReport {
	window := l.windowResolution()
	hardThreshold := autoCompactThreshold(window.Tokens)
	threshold, thresholdSource := l.effectiveCompactThreshold(hardThreshold, window.Source)
	r := ContextReport{
		Route: string(l.route), Model: l.modelID,
		Window: window.Tokens, WindowSource: window.Source,
		CompactThreshold: threshold, ThresholdSource: thresholdSource,
		Hidden: l.HiddenCount(),
	}
	if profile, ok := l.PrefillProfile(); ok {
		r.PrefillSamples = profile.Samples
		r.PrefillInput = profile.LastInputTokens
		r.PrefillCached = profile.LastCached
		r.PrefillEvaluated = profile.LastEvaluated
		r.PrefillTTFTMS = profile.LastTTFTMS
		r.PrefillTokensPerS = profile.LastTokensPerS
		if thresholdSource == "prefill-profile" {
			r.PrefillBudget = threshold
		}
	}
	u := l.SessionUsage()
	r.UsageIn, r.UsageOut = u.Input, u.Output
	visible := l.resolvedToolProviderView(l.VisibleMessages())
	r.Visible = len(visible)
	var items []ContextItem
	for i, m := range visible {
		t := llm.EstimateMessageTokens(m)
		r.EstimatedTokens += t
		switch m.Role {
		case llm.RoleSystem:
			r.SystemTokens += t
		case llm.RoleUser:
			r.UserTokens += t
		case llm.RoleAssistant:
			r.AssistantTokens += t
		case llm.RoleTool:
			r.ToolResultTokens += t
		}
		label := fmt.Sprintf("#%d %s", i, m.Role)
		if m.Role == llm.RoleTool && m.Name != "" {
			label += " (" + m.Name + ")"
		}
		label += ": " + legacyContextFirstWords(m.Content, 8)
		items = append(items, ContextItem{Label: label, Tokens: t})
	}
	if l.thinTools && l.route == RouteCoordinator {
		r.Thin = true
		_, tail := l.thinPartition()
		for _, t := range l.buildToolDefs() {
			r.ToolCount++
			st := (len(t.Name) + len(t.Description) + len(t.Schema)) / 4
			r.ToolSchemaTokens += st
			items = append(items, ContextItem{Label: "tool schema: " + t.Name, Tokens: st})
		}
		r.CatalogTokens = len(l.thinToolsPreamble()) / 4
		if r.CatalogTokens > 0 {
			items = append(items, ContextItem{
				Label: fmt.Sprintf("tool catalog (%d tail tools)", len(tail)), Tokens: r.CatalogTokens,
			})
		}
	} else {
		for _, t := range l.registry.Visible() {
			r.ToolCount++
			st := (len(t.Name) + len(t.Description) + len(t.Schema)) / 4
			r.ToolSchemaTokens += st
			items = append(items, ContextItem{Label: "tool schema: " + t.Name, Tokens: st})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Tokens > items[j].Tokens })
	if len(items) > 5 {
		items = items[:5]
	}
	r.Top = items
	estimate := l.nextRequestTokenEstimate()
	r.RequestTokens, r.RawRequestTokens = estimate.Effective, estimate.Raw
	r.RequestEstimateSource, r.ExactRequestBase = estimate.Source, estimate.ExactBase
	return r
}

func legacyContextFirstWords(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	words := strings.SplitN(s, " ", n+1)
	if len(words) > n {
		return strings.Join(words[:n], " ") + "…"
	}
	return s
}

func contextFirstWordsResult(fn func(string, int) string, s string, n int) (text string, panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	return fn(s, n), false
}

func TestContextFirstWordsPrefixMatchesLegacy(t *testing.T) {
	cases := []string{
		"", " \t\r\n\v\f ", "one", " one \t two\nthree ",
		"one two three four five six seven eight",
		"one two three four five six seven eight nine ten",
		"\u0085\u00a0one\u1680two\u2000three\u2001four\u2002five\u2003six\u2004seven\u2005eight\u2006nine\u2007ten\u2008eleven\u2009twelve\u200athirteen\u2028fourteen\u202ffifteen\u205fsixteen\u3000",
		"zażółć\u00a0gęślą\u2029jaźń\t界面 😀 seven eight nine ten",
		"a\x00b\u200bc\ufeffd e\xfff\t\xc0\xaf g h i j k l",
		"one two three four five six seven eight " + strings.Repeat("ninth", 100_000),
	}
	rng := rand.New(rand.NewSource(937))
	chunks := []string{"a", "界面", "żółć", "😀", "\xff", "\xc0\xaf", " ", "\t", "\n", "\v", "\f", "\r", "\u0085", "\u00a0", "\u2003", "\u2028", "\u2029", "\u3000", "\u200b", "\x00"}
	for i := 0; i < 1_000; i++ {
		var b strings.Builder
		for j, count := 0, rng.Intn(100); j < count; j++ {
			b.WriteString(chunks[rng.Intn(len(chunks))])
		}
		cases = append(cases, b.String())
	}
	for _, s := range cases {
		for _, n := range []int{-4, -1, 0, 1, 2, 8, 9, 32, int(^uint(0) >> 1)} {
			want, wantPanic := contextFirstWordsResult(legacyContextFirstWords, s, n)
			got, gotPanic := contextFirstWordsResult(firstWords, s, n)
			if got != want || gotPanic != wantPanic {
				t.Fatalf("firstWords(%q, %d) = %q, panic=%v; want %q, panic=%v", s, n, got, gotPanic, want, wantPanic)
			}
		}
	}
}

func legacyContextTopItems(candidates []contextTopItem, messages []llm.Message) []ContextItem {
	var items []ContextItem
	for _, candidate := range candidates {
		var label string
		switch candidate.kind {
		case contextTopMessage:
			m := messages[candidate.index]
			label = fmt.Sprintf("#%d %s", candidate.index, m.Role)
			if m.Role == llm.RoleTool && m.Name != "" {
				label += " (" + m.Name + ")"
			}
			label += ": " + legacyContextFirstWords(m.Content, 8)
		case contextTopSchema:
			label = "tool schema: " + candidate.name
		case contextTopCatalog:
			label = fmt.Sprintf("tool catalog (%d tail tools)", candidate.index)
		}
		items = append(items, ContextItem{Label: label, Tokens: candidate.tokens})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Tokens > items[j].Tokens })
	if len(items) > 5 {
		items = items[:5]
	}
	return items
}

func TestContextLazyTopPreservesStableOrder(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: " one\u00a0two three four five six seven eight nine "},
		{Role: llm.RoleTool, Name: "read_many", Content: " \t output "},
		{Role: llm.RoleAssistant, Content: "parts stay priced independently"},
	}
	rng := rand.New(rand.NewSource(873))
	for _, count := range []int{0, 1, 2, 4, 5, 6, 40, 300} {
		for run := 0; run < 40; run++ {
			var candidates []contextTopItem
			var top contextTopItems
			for i := 0; i < count; i++ {
				candidate := contextTopItem{kind: contextTopItemKind(i % 3), tokens: rng.Intn(5), index: i % len(messages), name: fmt.Sprintf("schema_%d", i)}
				candidates = append(candidates, candidate)
				top.add(candidate)
			}
			got, want := top.report(messages), legacyContextTopItems(candidates, messages)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("count=%d run=%d: top = %#v; want %#v", count, run, got, want)
			}
		}
	}
	// A tie at the cutoff retains messages before schemas before the catalog.
	candidates := []contextTopItem{
		{kind: contextTopMessage, index: 0, tokens: 20},
		{kind: contextTopMessage, index: 1, tokens: 20},
		{kind: contextTopMessage, index: 2, tokens: 20},
		{kind: contextTopSchema, name: "first", tokens: 20},
		{kind: contextTopSchema, name: "second", tokens: 20},
		{kind: contextTopCatalog, index: 17, tokens: 20},
	}
	var top contextTopItems
	for _, candidate := range candidates {
		top.add(candidate)
	}
	if got, want := top.report(messages), legacyContextTopItems(candidates, messages); !reflect.DeepEqual(got, want) {
		t.Fatalf("message/schema/catalog tie: top = %#v; want %#v", got, want)
	}
}

func contextReportLazyFixture(t testing.TB, thin bool, messageCount, outputRepeats int) *Loop {
	t.Helper()
	registry := tools.NewRegistry()
	noop := func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil }
	names := append([]string{}, thinCoreTools...)
	for i := 0; i < 16; i++ {
		names = append(names, fmt.Sprintf("tail_%02d", i))
	}
	for _, name := range names {
		registry.MustRegister(tools.Tool{
			Name: name, Description: "Read project results with Unicode \u00a0 descriptions.",
			Schema: `{"type":"object","properties":{"path":{"type":"string"},"limit":{"type":"integer","default":100}},"required":["path"]}`,
			Fn:     noop,
		})
		registry.MarkAlwaysOn(name)
	}
	l, err := NewLoop(LoopConfig{
		Provider: &stubProvider{name: "report-fixture"}, Registry: registry,
		System:    "Inspect the project and preserve the user's selected reasoning.",
		ThinTools: thin, StableToolset: true, CatalogHoist: thin,
	})
	if err != nil {
		t.Fatal(err)
	}
	l.route = RouteCoordinator
	for i := 0; i < messageCount; i++ {
		m := llm.Message{
			Role: llm.RoleTool, Name: "read_many",
			Content: strings.Repeat("    source line\twith results \u00a0 zażółć and context\r\n", outputRepeats+(i%3)),
		}
		if i%10 == 0 {
			m = llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("Inspect step %d and keep the current constraints", i)}
		} else if i%10 == 1 {
			m = llm.Message{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "Preserve structured parts"}}, ToolCalls: []llm.ToolCall{{ID: fmt.Sprintf("call_%d", i), Name: "read_many", Arguments: `{"paths":["src/main.go"]}`}}}
		}
		l.Messages = append(l.Messages, m)
	}
	l.providerMessages() // Freeze catalog exactly as a completed request does.
	l.recordContextBaseline(l.estimateNextRequestTokensRaw(), 2_000)
	return l
}

func TestContextReportLazyTopMatchesLegacy(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, route := range []RouteMode{RouteCoordinator, RouteChatOnly, RouteAdvisor, RouteClarify} {
			for _, hidden := range []bool{false, true} {
				t.Run(fmt.Sprintf("thin=%v/route=%s/hidden=%v", thin, route, hidden), func(t *testing.T) {
					l := contextReportLazyFixture(t, thin, 127, 128)
					l.route = route
					if hidden {
						l.hidden = make([]bool, len(l.Messages))
						for i := 2; i < len(l.hidden); i += 11 {
							l.hidden[i] = true
						}
					}
					messagesBefore := append([]llm.Message(nil), l.Messages...)
					hiddenBefore := append([]bool(nil), l.hidden...)
					want, got := legacyContextReportTopOracle(l), l.ContextReport()
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("report = %#v; want %#v", got, want)
					}
					if gotText, wantText := FormatContextReport(got), FormatContextReport(want); gotText != wantText {
						t.Fatalf("formatted report differs:\n%s\nwant:\n%s", gotText, wantText)
					}
					if !reflect.DeepEqual(l.Messages, messagesBefore) || !reflect.DeepEqual(l.hidden, hiddenBefore) {
						t.Fatal("report changed canonical messages or visibility")
					}
					// A late registry change retains the existing frozen catalog policy.
					l.registry.MustRegister(tools.Tool{Name: "late_schema", Description: strings.Repeat("large schema ", 300), Schema: `{}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
					l.registry.MarkAlwaysOn("late_schema")
					if got, want := l.ContextReport(), legacyContextReportTopOracle(l); !reflect.DeepEqual(got, want) {
						t.Fatalf("late schema report = %#v; want %#v", got, want)
					}
				})
			}
		}
	}
	// Fewer than five items and the nil top-list contract are tested separately.
	l := contextReportLazyFixture(t, false, 0, 0)
	l.Messages, l.hidden, l.registry = nil, nil, tools.NewRegistry()
	l.invalidateVisibleEstimate()
	if got, want := l.ContextReport(), legacyContextReportTopOracle(l); !reflect.DeepEqual(got, want) {
		t.Fatalf("empty report = %#v; want %#v", got, want)
	}
	l.Messages = []llm.Message{{Role: llm.RoleUser, Content: "one\u00a0two"}, {Role: llm.RoleTool, Content: ""}}
	if got, want := l.ContextReport(), legacyContextReportTopOracle(l); !reflect.DeepEqual(got, want) {
		t.Fatalf("small report = %#v; want %#v", got, want)
	}
}

var contextReportLazyBenchmarkSink ContextReport

func BenchmarkContextReportLazyTop(b *testing.B) {
	for _, thin := range []bool{false, true} {
		for _, outputRepeats := range []int{4, 1_024} {
			for _, legacy := range []bool{true, false} {
				b.Run(fmt.Sprintf("thin=%v/repeats=%d/legacy=%v", thin, outputRepeats, legacy), func(b *testing.B) {
					l := contextReportLazyFixture(b, thin, 127, outputRepeats)
					if got, want := l.ContextReport(), legacyContextReportTopOracle(l); !reflect.DeepEqual(got, want) {
						b.Fatal("benchmark fixture does not produce identical reports")
					}
					report := (*Loop).ContextReport
					if legacy {
						report = legacyContextReportTopOracle
					}
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						contextReportLazyBenchmarkSink = report(l)
					}
				})
			}
		}
	}
}
