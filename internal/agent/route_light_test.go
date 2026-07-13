package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// TestLoop_ChatRouteSendsMinimalToolSet: the chat route must send only
// tool_search + recall (when registered), never the full tool list.
func TestLoop_ChatRouteSendsMinimalToolSet(t *testing.T) {
	p := &captureProvider{name: "capture"}
	reg := tools.NewRegistry()
	noop := func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "x"}, nil
	}
	for _, name := range []string{"tool_search", "recall", "expensive_tool", "another_big_tool"} {
		reg.MustRegister(tools.Tool{Name: name, Description: "d " + name, Schema: `{"type":"object"}`, Fn: noop})
		reg.MarkAlwaysOn(name)
	}
	l := makeLoop(t, p, reg, "FULL COORDINATOR PROMPT")
	l.navigate = true
	ch, _ := l.Run(context.Background(), "hej")
	drainEvents(t, ch)

	if p.toolCount != 2 {
		t.Fatalf("toolCount=%d, want 2 (tool_search + recall)", p.toolCount)
	}
}

// TestLoop_CoordinatorRouteAppendsTimeStamp: every coordinator request
// ends with a fresh system message carrying date/time/zone.
func TestLoop_CoordinatorRouteAppendsTimeStamp(t *testing.T) {
	reg := tools.NewRegistry()
	l, err := NewLoop(LoopConfig{
		Provider: &stubProvider{name: "stub"},
		Registry: reg,
		System:   "base system",
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	l.route = RouteCoordinator
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "zrob cos"})
	msgs := l.providerMessages()
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleSystem || !strings.Contains(last.Content, "Current local date/time:") {
		t.Fatalf("last message = %+v, want trailing time-stamp system message", last)
	}
	if !strings.Contains(last.Content, "2026") {
		t.Errorf("time stamp missing year sentence: %q", last.Content)
	}
}

// TestLoop_ChatRouteStampNotInLeadingSystem locks in the cache fix for
// the chat-only/advisor routes: the minute-granular freshness stamp
// used to be concatenated into the LEADING system prompt, rewriting the
// prompt front (and killing the provider KV cache) every minute tick.
// It must now trail the request as the last message — same pattern as
// the coordinator route — where the provider demote pass renders it in
// place as a <system-reminder>.
func TestLoop_ChatRouteStampNotInLeadingSystem(t *testing.T) {
	reg := tools.NewRegistry()
	l, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "stub"}, Registry: reg, System: "base"})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "cześć"})

	for _, route := range []RouteMode{RouteChatOnly, RouteAdvisor, RouteClarify} {
		l.route = route
		msgs := l.providerMessages()
		if len(msgs) < 2 || msgs[0].Role != llm.RoleSystem {
			t.Fatalf("route %s: unexpected shape: %+v", route, msgs)
		}
		if strings.Contains(msgs[0].Content, "Current local date/time:") {
			t.Errorf("route %s: volatile time stamp baked into the leading system prompt", route)
		}
		last := msgs[len(msgs)-1]
		if last.Role != llm.RoleSystem || !strings.Contains(last.Content, "Current local date/time:") {
			t.Errorf("route %s: last message = %+v, want trailing time-stamp system message", route, last)
		}
		// Only the trailing message may carry the stamp.
		for i := 0; i < len(msgs)-1; i++ {
			if strings.Contains(msgs[i].Content, "Current local date/time:") {
				t.Errorf("route %s: time stamp found at index %d, must only be the final message", route, i)
			}
		}
	}
}

// TestLoop_BuildToolDefs_CoordinatorExposesAllVisibleWithSchema: the
// coordinator route must return every visible tool, preserving the
// full JSON Schema (the historical behaviour B2a extracted verbatim).
func TestLoop_BuildToolDefs_CoordinatorExposesAllVisibleWithSchema(t *testing.T) {
	reg := tools.NewRegistry()
	noop := func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "x"}, nil
	}
	for _, name := range []string{"tool_search", "recall", "edit_line", "darwin"} {
		reg.MustRegister(tools.Tool{Name: name, Description: "d " + name, Schema: `{"type":"object","properties":{"q":{"type":"string"}}}`, Fn: noop})
		reg.MarkAlwaysOn(name)
	}
	l := makeLoop(t, &stubProvider{name: "stub"}, reg, "base")
	l.route = RouteCoordinator

	defs := l.buildToolDefs()
	if len(defs) != 5 {
		t.Fatalf("coordinator defs = %d, want 5 (all visible + read_output)", len(defs))
	}
	for _, d := range defs {
		if !strings.Contains(d.Schema, "properties") {
			t.Errorf("tool %q lost its full schema: %q", d.Name, d.Schema)
		}
		if d.Description == "" {
			t.Errorf("tool %q lost its description", d.Name)
		}
	}
}

// TestLoop_BuildToolDefs_ChatRouteMinimalSet: chat/advisor routes must
// return only the chatRouteTools that are registered (tool_search +
// recall), never the full visible set.
func TestLoop_BuildToolDefs_ChatRouteMinimalSet(t *testing.T) {
	reg := tools.NewRegistry()
	noop := func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "x"}, nil
	}
	for _, name := range []string{"tool_search", "recall", "edit_line", "darwin"} {
		reg.MustRegister(tools.Tool{Name: name, Description: "d " + name, Schema: `{"type":"object"}`, Fn: noop})
		reg.MarkAlwaysOn(name)
	}
	l := makeLoop(t, &stubProvider{name: "stub"}, reg, "base")
	l.route = RouteChatOnly

	defs := l.buildToolDefs()
	if len(defs) != 2 {
		t.Fatalf("chat defs = %d, want 2 (tool_search + recall)", len(defs))
	}
	got := map[string]bool{}
	for _, d := range defs {
		got[d.Name] = true
	}
	if !got["tool_search"] || !got["recall"] {
		t.Errorf("chat route missing core tools: %v", got)
	}
	if got["edit_line"] || got["darwin"] {
		t.Errorf("chat route leaked tail tools: %v", got)
	}
}

// TestLoop_BuildToolDefs_ChatRouteSkipsUnregistered: chatRouteTools
// names that are not registered must be silently skipped, not panic.
func TestLoop_BuildToolDefs_ChatRouteSkipsUnregistered(t *testing.T) {
	reg := tools.NewRegistry()
	noop := func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "x"}, nil
	}
	// Register only tool_search; recall is absent.
	reg.MustRegister(tools.Tool{Name: "tool_search", Description: "d", Schema: `{"type":"object"}`, Fn: noop})
	reg.MarkAlwaysOn("tool_search")
	l := makeLoop(t, &stubProvider{name: "stub"}, reg, "base")
	l.route = RouteAdvisor

	defs := l.buildToolDefs()
	if len(defs) != 1 || defs[0].Name != "tool_search" {
		t.Fatalf("defs = %+v, want only tool_search", defs)
	}
}

// TestLoop_ChatRouteKeepsCurrentTurnToolPairs: tool calls made during
// the current chat-route turn must survive providerMessages so the
// provider sees valid call/result pairing.
func TestLoop_ChatRouteKeepsCurrentTurnToolPairs(t *testing.T) {
	reg := tools.NewRegistry()
	l, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "stub"}, Registry: reg, System: "base"})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	l.route = RouteChatOnly
	l.Messages = append(l.Messages,
		llm.Message{Role: llm.RoleUser, Content: "stare pytanie"},
		llm.Message{Role: llm.RoleAssistant, Content: "stara odpowiedz"},
		llm.Message{Role: llm.RoleUser, Content: "co slychac w go 1.25?"},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "tool_search", Arguments: `{"query":"web"}`}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Name: "tool_search", Content: "found web_search"},
	)
	msgs := l.providerMessages()
	var sawCall, sawResult bool
	for _, m := range msgs {
		if len(m.ToolCalls) > 0 {
			sawCall = true
		}
		if m.Role == llm.RoleTool {
			sawResult = true
		}
	}
	if !sawCall || !sawResult {
		t.Fatalf("tool pairing lost: call=%v result=%v msgs=%+v", sawCall, sawResult, msgs)
	}
}

// thinLoop builds a coordinator loop with thin tools on and a
// registry of core + tail tools, all always-on (as in production).
func thinLoop(t *testing.T) *Loop {
	t.Helper()
	reg := tools.NewRegistry()
	noop := func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "x"}, nil
	}
	// core + tail, all always-on.
	for _, name := range []string{"tool_search", "invoke_tool", "edit_line", "read_context", "read_lines", "read_many", "read_image", "search_code", "ctx_execute", "ask_user", "recall", "list_dir", "darwin", "web_search", "read_pdf"} {
		reg.MustRegister(tools.Tool{Name: name, Description: "does " + name + " things for the user", Schema: `{"type":"object","properties":{"q":{"type":"string"}}}`, Fn: noop})
		reg.MarkAlwaysOn(name)
	}
	l, err := NewLoop(LoopConfig{
		Provider:  &stubProvider{name: "stub"},
		Registry:  reg,
		System:    "base",
		ThinTools: true,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	l.route = RouteCoordinator
	return l
}

// TestThinTools_OnlyCoreGetsFullSchema: with thin tools on, the
// dormant tail must NOT appear in toolDefs; only the core does.
func TestThinTools_OnlyCoreGetsFullSchema(t *testing.T) {
	l := thinLoop(t)
	defs := l.buildToolDefs()
	got := map[string]bool{}
	for _, d := range defs {
		got[d.Name] = true
	}
	for _, core := range thinCoreTools {
		if !got[core] {
			t.Errorf("core tool %q missing from toolDefs", core)
		}
	}
	for _, tail := range []string{"darwin", "web_search", "read_pdf"} {
		if got[tail] {
			t.Errorf("dormant tail tool %q leaked into toolDefs (should be catalog-only)", tail)
		}
	}
}

// TestThinTools_TailAdvertisedInCatalog: dormant tail tools must
// appear in the injected catalog section of the thin preamble.
func TestThinTools_TailAdvertisedInCatalog(t *testing.T) {
	l := thinLoop(t)
	pre := l.thinToolsPreamble()
	for _, tail := range []string{"darwin", "web_search", "read_pdf"} {
		if !strings.Contains(pre, tail) {
			t.Errorf("tail tool %q missing from preamble:\n%s", tail, pre)
		}
	}
	// Isolate the catalog section (after its header). The format
	// instruction above it legitimately names a core tool as an
	// example, so we only assert against the catalog body.
	const hdr = "loadable on demand"
	idx := strings.Index(pre, hdr)
	if idx < 0 {
		t.Fatalf("catalog header missing from preamble:\n%s", pre)
	}
	catalog := pre[idx:]
	// Core tools must NOT be re-advertised in the catalog body.
	for _, core := range []string{"ctx_execute", "read_lines", "read_context"} {
		if strings.Contains(catalog, core) {
			t.Errorf("core tool %q should not appear in catalog body", core)
		}
	}
}

// TestThinTools_ActivatedTailGetsSchemaLeavesCatalog: once the model
// pulls a tail tool in via tool_search (Activate), it must move from
// the catalog into toolDefs with a full schema.
func TestThinTools_ActivatedTailGetsSchemaLeavesCatalog(t *testing.T) {
	l := thinLoop(t)
	l.registry.Activate("web_search")

	defs := l.buildToolDefs()
	got := map[string]bool{}
	for _, d := range defs {
		got[d.Name] = true
	}
	if !got["web_search"] {
		t.Error("activated tail tool web_search must be in toolDefs with full schema")
	}
	if got["darwin"] {
		t.Error("still-dormant darwin must stay out of toolDefs")
	}
	cat := l.thinToolsPreamble()
	if strings.Contains(cat, "web_search") {
		t.Error("activated web_search should no longer be in the catalog")
	}
	if !strings.Contains(cat, "darwin") {
		t.Error("dormant darwin should still be in the catalog")
	}
}

// TestThinTools_DisabledPreservesHistoricalBehaviour: with thin
// tools OFF, every visible tool gets a full schema and no catalog
// is injected.
func TestThinTools_DisabledPreservesHistoricalBehaviour(t *testing.T) {
	l := thinLoop(t)
	l.thinTools = false

	defs := l.buildToolDefs()
	if want := len(l.registry.Visible()); len(defs) != want {
		t.Errorf("thin-off coordinator defs = %d, want %d (all visible)", len(defs), want)
	}
	if cat := l.thinToolsPreamble(); cat != "" {
		t.Errorf("thin-off must inject no catalog, got:\n%s", cat)
	}
}

// TestThinTools_CatalogInjectedBeforeTimestamp: the catalog must be
// a system message and the freshness stamp must remain last (so the
// cacheable prefix is preserved).
func TestThinTools_CatalogInjectedBeforeTimestamp(t *testing.T) {
	l := thinLoop(t)
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "zrob cos"})
	msgs := l.providerMessages()
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, "Current local date/time:") {
		t.Fatalf("last message must be the time stamp, got: %q", last.Content)
	}
	// the message before last should be the thin preamble (instruction + catalog).
	prev := msgs[len(msgs)-2]
	if !strings.Contains(prev.Content, "loadable on demand") {
		t.Errorf("catalog should sit just before the time stamp, got: %q", prev.Content)
	}
}

// TestThinTools_PreambleTeachesSentinelFormat: under thin tools the
// preamble MUST instruct the model to call tools with the « » format,
// otherwise the wired parser waits on syntax the model never emits.
func TestThinTools_PreambleTeachesSentinelFormat(t *testing.T) {
	l := thinLoop(t)
	pre := l.thinToolsPreamble()
	if !strings.Contains(pre, "\u00AB") || !strings.Contains(pre, "\u00BB") {
		t.Errorf("preamble must show the « » sentinels, got:\n%s", pre)
	}
	if !strings.Contains(pre, "not JSON") {
		t.Errorf("preamble must tell the model not to use JSON, got:\n%s", pre)
	}
}

// TestThinTools_PreambleInstructionWithoutTail: even when the tail is
// empty (everything activated), the call-format instruction must
// still be present — it governs how the core tools are called.
func TestThinTools_PreambleInstructionWithoutTail(t *testing.T) {
	l := thinLoop(t)
	// Activate every non-core tool so the tail is empty.
	l.registry.Activate("darwin", "web_search", "read_pdf")
	pre := l.thinToolsPreamble()
	if pre == "" {
		t.Fatal("preamble must not be empty: format instruction still applies")
	}
	if !strings.Contains(pre, "\u00AB") {
		t.Errorf("format instruction missing when tail empty:\n%s", pre)
	}
	if strings.Contains(pre, "loadable on demand") {
		t.Errorf("no catalog should appear when tail is empty:\n%s", pre)
	}
}
