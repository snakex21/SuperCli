package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func contextTestLoop(t *testing.T) *Loop {
	t.Helper()
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "dummy",
		Description: "a dummy tool for tests",
		Schema:      `{"type":"object"}`,
		Fn:          func(ctx context.Context, args json.RawMessage) (tools.Result, error) { return tools.Result{}, nil },
	})
	reg.MarkAlwaysOn("dummy")
	l, err := NewLoop(LoopConfig{
		Provider: &stubProvider{name: "test-model"},
		Registry: reg,
		System:   strings.Repeat("system prompt ", 50),
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	return l
}

func TestLastTurnPromptTokens(t *testing.T) {
	l := contextTestLoop(t)
	if _, ok := l.LastTurnPromptTokens(); ok {
		t.Fatal("no turn yet: ok should be false")
	}
	l.sessUsageMu.Lock()
	l.lastTurnPrompt = 4096
	l.lastTurnSet = true
	l.sessUsageMu.Unlock()
	got, ok := l.LastTurnPromptTokens()
	if !ok || got != 4096 {
		t.Fatalf("LastTurnPromptTokens() = %d, %v; want 4096, true", got, ok)
	}
}

func TestLastTurnBreakdown(t *testing.T) {
	l := contextTestLoop(t)
	if _, _, _, ok := l.LastTurnBreakdown(); ok {
		t.Fatal("no turn yet: ok should be false")
	}
	l.sessUsageMu.Lock()
	l.lastTurnPrompt = 14580
	l.lastTurnCached = 14200
	l.lastTurnOutput = 220
	l.lastTurnSet = true
	l.sessUsageMu.Unlock()
	cached, evaluated, generated, ok := l.LastTurnBreakdown()
	if !ok || cached != 14200 || evaluated != 380 || generated != 220 {
		t.Fatalf("LastTurnBreakdown() = %d/%d/%d/%v; want 14200/380/220/true",
			cached, evaluated, generated, ok)
	}
}

func TestContextReport_Breakdown(t *testing.T) {
	l := contextTestLoop(t)
	l.Messages = append(l.Messages,
		llm.Message{Role: llm.RoleUser, Content: strings.Repeat("user ", 100)},
		llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("reply ", 50)},
		llm.Message{Role: llm.RoleTool, Name: "dummy", Content: strings.Repeat("output ", 200)},
	)
	r := l.ContextReport()
	if r.Visible != 4 {
		t.Errorf("Visible = %d, want 4", r.Visible)
	}
	if r.SystemTokens == 0 || r.UserTokens == 0 || r.AssistantTokens == 0 || r.ToolResultTokens == 0 {
		t.Errorf("breakdown has zeros: %+v", r)
	}
	if r.ToolCount != 1 || r.ToolSchemaTokens == 0 {
		t.Errorf("tool schema accounting: count=%d tokens=%d", r.ToolCount, r.ToolSchemaTokens)
	}
	if len(r.Top) == 0 || len(r.Top) > 5 {
		t.Errorf("Top len = %d", len(r.Top))
	}
	// Largest item should be the tool result (~350 tok).
	if !strings.Contains(r.Top[0].Label, "tool (dummy)") {
		t.Errorf("Top[0] = %+v, want the tool result first", r.Top[0])
	}
	out := FormatContextReport(r)
	for _, want := range []string{"tool schemas", "system prompt", "top items"} {
		if !strings.Contains(out, want) {
			t.Errorf("formatted report missing %q:\n%s", want, out)
		}
	}
}

// thinReportLoop is like thinLoop but registers tools with
// realistically-sized JSON schemas (the long tail is where thin mode
// pays off — see the production /context measurement). The dormant
// tail carries large schemas the thin protocol drops in favour of a
// one-line catalog entry each.
func thinReportLoop(t *testing.T) *Loop {
	t.Helper()
	reg := tools.NewRegistry()
	noop := func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "x"}, nil
	}
	// A schema roughly the size of a real multi-arg tool (~400 chars).
	bigSchema := `{"type":"object","properties":{` +
		`"path":{"type":"string","description":"absolute path to the target file"},` +
		`"query":{"type":"string","description":"natural-language search query for the operation"},` +
		`"limit":{"type":"integer","description":"maximum number of results to return"}},` +
		`"required":["path"]}`
	for _, name := range []string{"tool_search", "invoke_tool", "edit_line", "read_context", "read_lines", "read_many", "ctx_execute", "recall", "list_dir", "darwin", "web_search", "read_pdf"} {
		reg.MustRegister(tools.Tool{
			Name:        name,
			Description: "performs " + name + " operations on behalf of the user; see schema for arguments",
			Schema:      bigSchema,
			Fn:          noop,
		})
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

// TestContextReport_ThinMode_AccountsCatalogNotSchemas: under the
// thin tool protocol /context must charge only the schema-carrying
// core tools plus the compact catalog — NOT the full schema of every
// visible tool. Otherwise the report hides the very saving thin mode
// buys. This compares the same registry in thin vs JSON mode.
func TestContextReport_ThinMode_AccountsCatalogNotSchemas(t *testing.T) {
	// Use realistically-sized schemas: the thin protocol trades a
	// fixed per-turn preamble (the sentinel call-format instruction)
	// for dropped schemas, so it only nets a saving once the dropped
	// tail schemas outweigh that fixed cost — which is the real-world
	// case (production schemas are hundreds of chars each).
	thin := thinReportLoop(t)
	rThin := thin.ContextReport()

	if !rThin.Thin {
		t.Fatal("report.Thin should be true under ThinTools on coordinator route")
	}
	if rThin.CatalogTokens == 0 {
		t.Error("CatalogTokens should be > 0 (dormant tail advertised in catalog)")
	}
	// Only the core carries a full schema; the tail does not.
	if rThin.ToolCount != len(thinCoreTools) {
		t.Errorf("thin ToolCount = %d, want %d (core only)", rThin.ToolCount, len(thinCoreTools))
	}

	// JSON baseline: same registry, thin off => every visible tool's
	// full schema is counted and there is no catalog.
	json := thinReportLoop(t)
	json.thinTools = false
	rJSON := json.ContextReport()
	if rJSON.Thin {
		t.Error("report.Thin should be false when ThinTools is off")
	}
	if rJSON.CatalogTokens != 0 {
		t.Errorf("JSON mode CatalogTokens = %d, want 0", rJSON.CatalogTokens)
	}

	thinTotal := rThin.ToolSchemaTokens + rThin.CatalogTokens
	jsonTotal := rJSON.ToolSchemaTokens
	if thinTotal >= jsonTotal {
		t.Errorf("thin tool cost (%d) must be < JSON tool cost (%d)", thinTotal, jsonTotal)
	}

	out := FormatContextReport(rThin)
	if !strings.Contains(out, "tool catalog (thin)") {
		t.Errorf("thin report must show the catalog line:\n%s", out)
	}
}
