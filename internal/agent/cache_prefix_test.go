package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// serializeForCache renders a message slice the way a chat template
// lays messages into the prompt, so two turns can be compared for
// KV-cache prefix reuse. It is intentionally simple: role + content +
// part text + tool-call name/args, one block per message.
func serializeForCache(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(string(m.Role))
		b.WriteByte('\n')
		b.WriteString(m.Content)
		for _, p := range m.Parts {
			b.WriteString(p.Text)
		}
		for _, tc := range m.ToolCalls {
			b.WriteString(tc.Name)
			b.WriteString(tc.Arguments)
		}
		b.WriteString("\n---\n")
	}
	return b.String()
}

func newCacheTestLoop(t *testing.T) *Loop {
	t.Helper()
	reg := tools.NewRegistry()
	noop := func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "x"}, nil
	}
	// A couple of thin-core tools plus a dormant tail tool so the
	// thin-tools partition has something to move on activation.
	for _, name := range []string{"tool_search", "recall", "list_dir", "edit_line"} {
		reg.MustRegister(tools.Tool{Name: name, Description: "core " + name, Schema: `{"type":"object","properties":{"q":{"type":"string"}}}`, Fn: noop})
		reg.MarkAlwaysOn(name)
	}
	// web_search is registered but NOT always-on: it is a dormant
	// tail tool the model can pull in via tool_search.
	reg.MustRegister(tools.Tool{Name: "web_search", Description: "search the web", Schema: `{"type":"object","properties":{"query":{"type":"string"}}}`, Fn: noop})

	l := makeLoop(t, &stubProvider{name: "stub"}, reg, "STABLE BASE SYSTEM PROMPT")
	l.route = RouteCoordinator
	l.thinTools = true
	return l
}

// TestCache_CoordinatorHistoryIsAppendOnlyPrefix locks in the core
// KV-cache invariant: the conversation history (the bulk of the
// prompt) grows append-only, so turn N's serialized history is a
// literal prefix of turn N+1's. Any change that mutates, reorders, or
// injects volatile content into earlier history would break prompt
// caching on the slow local backend and trips this test.
func TestCache_CoordinatorHistoryIsAppendOnlyPrefix(t *testing.T) {
	l := newCacheTestLoop(t)

	// Turn N history.
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "pierwsze zadanie"})
	snapshotN := serializeForCache(l.VisibleMessages())

	// The model answers and calls a tool; the user follows up. This
	// is exactly how a real turn extends history: append-only.
	l.Messages = append(l.Messages,
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "list_dir", Arguments: `{"path":"."}`}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "1", Content: "a.go\nb.go"},
		llm.Message{Role: llm.RoleUser, Content: "drugie zadanie"},
	)
	snapshotN1 := serializeForCache(l.VisibleMessages())

	if !strings.HasPrefix(snapshotN1, snapshotN) {
		t.Fatalf("turn N history is not a literal prefix of turn N+1;\nN:\n%q\nN+1:\n%q", snapshotN, snapshotN1)
	}
	// The cacheable history must not carry the per-turn freshness
	// stamp (that belongs at the very end, outside the stable prefix).
	if strings.Contains(snapshotN, "Current local date/time:") {
		t.Error("volatile time stamp leaked into the cacheable history prefix")
	}
}

// TestCache_VolatileContentStaysAtEnd guards that the only per-turn
// volatile content (the time stamp) is the LAST message and the thin
// preamble is second-to-last, so the whole leading history stays
// byte-stable for prompt caching.
func TestCache_VolatileContentStaysAtEnd(t *testing.T) {
	l := newCacheTestLoop(t)
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "zrob cos"})

	msgs := l.providerMessages()
	if len(msgs) < 3 {
		t.Fatalf("providerMessages too short: %d", len(msgs))
	}
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleSystem || !strings.Contains(last.Content, "Current local date/time:") {
		t.Fatalf("last message must be the volatile time stamp, got %+v", last)
	}
	// With thin tools on and a dormant tail, the second-to-last
	// message is the thin preamble (also injected, also at the end).
	pre := msgs[len(msgs)-2]
	if pre.Role != llm.RoleSystem {
		t.Fatalf("second-to-last message should be the injected thin preamble, got role %q", pre.Role)
	}
	// Nothing before the injected tail may carry the time stamp.
	for i := 0; i < len(msgs)-1; i++ {
		if strings.Contains(msgs[i].Content, "Current local date/time:") {
			t.Errorf("time stamp found at index %d, must only be the final message", i)
		}
	}
}

// TestCache_ToolDefsDeterministicWithoutActivation locks in that the
// tool-definition list (which a chat template serializes near the
// START of the prompt) is byte-identical turn to turn when no new
// tool is activated. Non-determinism here (e.g. map iteration order)
// would silently invalidate the whole cached prefix every turn.
func TestCache_ToolDefsDeterministicWithoutActivation(t *testing.T) {
	l := newCacheTestLoop(t)

	ser := func(defs []llm.ToolDef) string {
		var b strings.Builder
		for _, d := range defs {
			b.WriteString(d.Name)
			b.WriteByte('\x00')
			b.WriteString(d.Description)
			b.WriteByte('\x00')
			b.WriteString(d.Schema)
			b.WriteByte('\n')
		}
		return b.String()
	}

	first := ser(l.buildToolDefs())
	for i := 0; i < 20; i++ {
		if got := ser(l.buildToolDefs()); got != first {
			t.Fatalf("buildToolDefs not deterministic on call %d:\nwant %q\ngot  %q", i, first, got)
		}
	}
}

// TestCache_ActivationGrowsToolDefs documents (and pins) the ONE known
// KV-cache invalidation point in the thin protocol's DEFAULT mode:
// pulling in a dormant tail tool via tool_search promotes it into the
// request tool list, which a chat template serializes near the prompt
// start — so the cached prefix is invalidated for that one turn. The
// stableToolset mode removes exactly this point (see the tests below);
// this test pins the default so the two modes stay deliberate.
func TestCache_ActivationGrowsToolDefs(t *testing.T) {
	l := newCacheTestLoop(t) // stableToolset defaults to false

	before := len(l.buildToolDefs())
	// Simulate what tool_search does when it matches web_search.
	l.registry.Activate("web_search")
	after := len(l.buildToolDefs())

	if after != before+1 {
		t.Fatalf("activation should grow the tool list by 1 (known cache-invalidation point); before=%d after=%d", before, after)
	}
}

// TestCache_StableToolset_ActivationKeepsToolDefsByteStable pins the
// stableToolset guarantee: activating a tail tool via tool_search must
// NOT change the request tool list in any way — same length, same
// order, same bytes — so the server-side KV prompt cache survives the
// activation. The schema still reaches the model as the tool_search
// result text at the (cache-safe) end of history.
func TestCache_StableToolset_ActivationKeepsToolDefsByteStable(t *testing.T) {
	l := newCacheTestLoop(t)
	l.stableToolset = true

	ser := func(defs []llm.ToolDef) string {
		var b strings.Builder
		for _, d := range defs {
			b.WriteString(d.Name)
			b.WriteByte('\x00')
			b.WriteString(d.Description)
			b.WriteByte('\x00')
			b.WriteString(d.Schema)
			b.WriteByte('\n')
		}
		return b.String()
	}

	before := ser(l.buildToolDefs())
	l.registry.Activate("web_search")
	after := ser(l.buildToolDefs())

	if before != after {
		t.Fatalf("stableToolset: activation changed the tool defs;\nbefore:\n%q\nafter:\n%q", before, after)
	}
	// The activated tool must still be advertised somewhere the model
	// can see (the thin catalog), not silently dropped.
	if !strings.Contains(l.thinToolsPreamble(), "web_search") {
		t.Error("stableToolset: activated tail tool missing from the thin catalog")
	}
}

// TestCache_StableToolset_PreambleHoistedIntoStablePrefix pins the
// hoist: with stableToolset on, the thin preamble (sentinel instruction
// + catalog) lives in the leading system block of the prompt — inside
// the KV-cacheable prefix — and is NOT re-injected behind the growing
// history. Across steps the serialized prompt (minus the volatile
// trailing stamp) of step N must be a literal byte prefix of step N+1.
func TestCache_StableToolset_PreambleHoistedIntoStablePrefix(t *testing.T) {
	l := newCacheTestLoop(t)
	l.stableToolset = true
	l.catalogHoist = true
	// Make web_search a visible dormant-tail tool so the catalog is
	// non-empty (in the fixture it is registered but not always-on).
	l.registry.MarkAlwaysOn("web_search")
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "pierwsze zadanie"})

	stripStamp := func(msgs []llm.Message) []llm.Message {
		last := msgs[len(msgs)-1]
		if !strings.Contains(last.Content, "Current local date/time:") {
			t.Fatalf("last message must be the volatile stamp, got %+v", last)
		}
		return msgs[:len(msgs)-1]
	}

	stepN := stripStamp(l.providerMessages())
	// Strict local templates accept one leading system message only. The
	// stable base and hoisted catalog must therefore be merged at index 0.
	if stepN[0].Role != llm.RoleSystem ||
		!strings.Contains(stepN[0].Content, "STABLE BASE SYSTEM PROMPT") ||
		!strings.Contains(stepN[0].Content, "web_search") {
		t.Fatalf("merged base+catalog system expected at index 0, got %+v", stepN[0])
	}
	if len(stepN) > 1 && stepN[1].Role == llm.RoleSystem {
		t.Fatalf("strict-template hoist left a second leading system message: %+v", stepN[:2])
	}
	// No trailing copy behind the history.
	tail := stepN[len(stepN)-1]
	if strings.Contains(tail.Content, "loadable on demand") {
		t.Error("preamble must not be duplicated at the end of the prompt when hoisted")
	}

	// Step N+1: history grew append-only; even a tool activation must
	// not move or rewrite the hoisted preamble.
	l.registry.Activate("web_search")
	l.Messages = append(l.Messages,
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "list_dir", Arguments: `{"path":"."}`}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "1", Content: "a.go"},
	)
	stepN1 := stripStamp(l.providerMessages())

	if !strings.HasPrefix(serializeForCache(stepN1), serializeForCache(stepN)) {
		t.Fatalf("stableToolset hoist: step N prompt is not a byte prefix of step N+1;\nN:\n%q\nN+1:\n%q",
			serializeForCache(stepN), serializeForCache(stepN1))
	}
}

// TestCache_HoistOff_PreambleStaysAtTail pins the default behaviour:
// without catalogHoist (even with stableToolset on) the preamble keeps
// its historical recency placement just before the freshness stamp.
func TestCache_HoistOff_PreambleStaysAtTail(t *testing.T) {
	l := newCacheTestLoop(t) // catalogHoist defaults to false
	l.stableToolset = true
	l.registry.MarkAlwaysOn("web_search")
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "zrob cos"})

	msgs := l.providerMessages()
	pre := msgs[len(msgs)-2]
	if pre.Role != llm.RoleSystem || !strings.Contains(pre.Content, "web_search") {
		t.Fatalf("stableToolset off: preamble must sit second-to-last, got %+v", pre)
	}
	if strings.Contains(msgs[1].Content, "web_search") && msgs[1].Role == llm.RoleSystem {
		t.Error("stableToolset off: preamble must not be hoisted into the prefix")
	}
}

// TestCache_StableToolset_TailToolExecutesWithoutPromotion proves the
// promotion is redundant for execution: with stableToolset on, a tail
// tool activated via tool_search is absent from the request tool list
// yet a model call by name still executes end-to-end through the
// loop's invoke path (name hardening + Registry.Execute dispatch by
// name, not by tools-list membership).
func TestCache_StableToolset_TailToolExecutesWithoutPromotion(t *testing.T) {
	l := newCacheTestLoop(t)
	l.stableToolset = true
	l.registry.Activate("web_search")

	for _, d := range l.buildToolDefs() {
		if d.Name == "web_search" {
			t.Fatal("stableToolset: web_search must NOT be promoted into the tool defs")
		}
	}

	out := make(chan Event, 8)
	res := l.invoke(context.Background(), llm.ToolCall{ID: "tc1", Name: "web_search", Arguments: `{"query":"golang"}`}, out)
	if res.err != nil {
		t.Fatalf("invoke returned error: %v", res.err)
	}
	if len(res.followUps) != 1 {
		t.Fatalf("expected 1 follow-up tool message, got %d", len(res.followUps))
	}
	fu := res.followUps[0]
	if fu.Role != llm.RoleTool || fu.Name != "web_search" || fu.Content != "x" {
		t.Fatalf("unexpected follow-up: %+v", fu)
	}
}
