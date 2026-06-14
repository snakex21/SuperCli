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
	ch, _ := l.Run(context.Background(), "cześć")
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
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "zrób coś"})
	msgs := l.providerMessages()
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleSystem || !strings.Contains(last.Content, "Current local date/time:") {
		t.Fatalf("last message = %+v, want trailing time-stamp system message", last)
	}
	if !strings.Contains(last.Content, "2026") {
		t.Errorf("time stamp missing year sentence: %q", last.Content)
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
	if len(defs) != 4 {
		t.Fatalf("coordinator defs = %d, want 4 (all visible)", len(defs))
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
		llm.Message{Role: llm.RoleAssistant, Content: "stara odpowiedź"},
		llm.Message{Role: llm.RoleUser, Content: "co słychać w go 1.25?"},
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
	for _, name := range []string{"tool_search", "edit_line", "read_context", "read_lines", "ctx_execute", "recall", "darwin", "web_search", "read_pdf"} {
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
// appear in the injected catalog system message.
func TestThinTools_TailAdvertisedInCatalog(t *testing.T) {
	l := thinLoop(t)
	cat := l.toolCatalog()
	for _, tail := range []string{"darwin", "web_search", "read_pdf"} {
		if !strings.Contains(cat, tail) {
			t.Errorf("tail tool %q missing from catalog:\n%s", tail, cat)
		}
	}
	// Core tools must NOT be re-advertised in the catalog.
	for _, core := range []string{"edit_line", "ctx_execute"} {
		if strings.Contains(cat, core) {
			t.Errorf("core tool %q should not appear in catalog", core)
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
	cat := l.toolCatalog()
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
	if len(defs) != 9 {
		t.Errorf("thin-off coordinator defs = %d, want 9 (all visible)", len(defs))
	}
	if cat := l.toolCatalog(); cat != "" {
		t.Errorf("thin-off must inject no catalog, got:\n%s", cat)
	}
}

// TestThinTools_CatalogInjectedBeforeTimestamp: the catalog must be
// a system message and the freshness stamp must remain last (so the
// cacheable prefix is preserved).
func TestThinTools_CatalogInjectedBeforeTimestamp(t *testing.T) {
	l := thinLoop(t)
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "zrób coś"})
	msgs := l.providerMessages()
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, "Current local date/time:") {
		t.Fatalf("last message must be the time stamp, got: %q", last.Content)
	}
	// the message before last should be the catalog.
	prev := msgs[len(msgs)-2]
	if !strings.Contains(prev.Content, "Additional tools available") {
		t.Errorf("catalog should sit just before the time stamp, got: %q", prev.Content)
	}
}
