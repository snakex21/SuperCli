package webgui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/memory"
)

func webLiveBlocksText(blocks []llm.ContextBlock) string {
	var text strings.Builder
	for _, block := range blocks {
		text.WriteString(block.Text)
	}
	return text.String()
}

type liveContextFixtureProvider struct {
	requests [][]llm.Message
}

func (*liveContextFixtureProvider) Name() string { return "echo-live-context-fixture" }
func (p *liveContextFixtureProvider) Complete(_ context.Context, messages []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.requests = append(p.requests, append([]llm.Message(nil), messages...))
	out := make(chan llm.Delta, 2)
	if len(p.requests) == 1 {
		out <- llm.Delta{ToolCall: &llm.ToolCall{ID: "remember", Name: "remember", Arguments: `{"scope":"global","type":"preference","text":"User fixture preference: preserve zażółć, 🙂 and 東京 in output."}`}}
		out <- llm.Delta{ToolCall: &llm.ToolCall{ID: "abandon", Name: "goal", Arguments: `{"action":"abandon"}`}, FinishReason: "tool_calls"}
	} else {
		out <- llm.Delta{Content: "Saved the preference and closed the fixture goal.", FinishReason: "stop"}
	}
	close(out)
	return out, nil
}

func TestWebLiveContextRefreshesMemoryAndGoalWithinOneRun(t *testing.T) {
	e, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	ctx := context.Background()
	goals, err := e.goalServiceAt(ctx, e.Home())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := goals.Set(ctx, "ACTIVE_GOAL_FIXTURE", "", "", ""); err != nil {
		t.Fatal(err)
	}
	provider := &liveContextFixtureProvider{}
	e.prov = provider
	loop, err := e.newLoop()
	if err != nil {
		t.Fatal(err)
	}
	events, err := loop.Run(ctx, "Save the fixture preference and abandon this goal.")
	if err != nil {
		t.Fatal(err)
	}
	for event := range events {
		if failure, ok := event.(agent.ErrorEvent); ok {
			t.Fatal(failure.Err)
		}
	}
	if len(provider.requests) != 2 {
		t.Fatalf("model calls=%d, want 2", len(provider.requests))
	}
	before := provider.requests[0][len(provider.requests[0])-1].Content
	after := provider.requests[1][len(provider.requests[1])-1].Content
	if !strings.Contains(before, "ACTIVE_GOAL_FIXTURE") || strings.Contains(after, "[current_goal]") {
		t.Fatal("GUI kept the startup active-goal snapshot after abandon")
	}
	if strings.Contains(before, "User fixture preference:") || strings.Count(after, "User fixture preference:") != 1 {
		t.Fatal("GUI did not replace memory by the next provider request")
	}
	if !strings.Contains(after, "zażółć") || !strings.Contains(after, "🙂") || !strings.Contains(after, "東京") {
		t.Fatal("GUI refresh lost unique Unicode data")
	}
	for _, message := range loop.AllMessages() {
		if strings.Contains(message.Content, "[memory_briefing]") || strings.Contains(message.Content, "[current_goal]") {
			t.Fatal("GUI persisted live snapshots as conversation turns")
		}
	}
	t.Logf("same two-call fixture: startup snapshot active goal=1 -> next snapshot=0; memory additions next snapshot=1; model calls=2; before_tail_bytes=%d after_tail_bytes=%d", len(before), len(after))
}

func TestWebLiveContextScopePinnedAcrossRootAndSessionChanges(t *testing.T) {
	ctx := context.Background()
	firstRoot, secondRoot := t.TempDir(), t.TempDir()
	e, err := NewEngine(echoConfig(), firstRoot, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	_, firstStore := e.webMemoryStores(firstRoot)
	_, secondStore := e.webMemoryStores(secondRoot)
	if firstStore == nil || secondStore == nil {
		t.Fatal("fixture memory stores unavailable")
	}
	for _, entry := range []memory.Entry{
		{ID: "fact-a", Scope: memory.ScopeFact, Content: "ROOT_A_MEMORY", Source: memory.SourceUser},
		{ID: "web-session-A", Scope: memory.ScopeTaskLog, Content: "OWN_SESSION_A_CAPSULE", Source: memory.SourceUser},
		{ID: "web-session-B", Scope: memory.ScopeTaskLog, Content: "OTHER_SESSION_B_CAPSULE", Source: memory.SourceUser},
	} {
		if err := firstStore.Put(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := secondStore.Put(memory.Entry{ID: "fact-b", Scope: memory.ScopeFact, Content: "ROOT_B_MEMORY", Source: memory.SourceUser}); err != nil {
		t.Fatal(err)
	}
	firstGoals, err := e.goalServiceAt(ctx, firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := firstGoals.Set(ctx, "ROOT_A_GOAL", "", "", ""); err != nil {
		t.Fatal(err)
	}
	secondGoals, err := e.goalServiceAt(ctx, secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secondGoals.Set(ctx, "ROOT_B_GOAL", "", "", ""); err != nil {
		t.Fatal(err)
	}
	captured := e.webLiveContextBlocks(firstRoot, 1400, "A", firstGoals, false)
	// Simulate selecting a different root while the previous run owns its tools.
	e.mu.Lock()
	e.home = secondRoot
	e.mu.Unlock()
	blocks, err := captured(ctx)
	if err != nil {
		t.Fatal(err)
	}
	text := webLiveBlocksText(blocks)
	for _, wanted := range []string{"ROOT_A_MEMORY", "ROOT_A_GOAL", "OTHER_SESSION_B_CAPSULE", firstRoot} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("captured run lost %q", wanted)
		}
	}
	for _, unwanted := range []string{"ROOT_B_MEMORY", "ROOT_B_GOAL", "OWN_SESSION_A_CAPSULE"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("captured run gained unrelated %q", unwanted)
		}
	}
	// A newly resumed session on the first root excludes its own capsule instead.
	resumed := e.webLiveContextBlocks(firstRoot, 1400, "B", firstGoals, false)
	blocks, err = resumed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	text = webLiveBlocksText(blocks)
	if !strings.Contains(text, "OWN_SESSION_A_CAPSULE") || strings.Contains(text, "OTHER_SESSION_B_CAPSULE") {
		t.Fatal("resumed session used another session's capsule exclusion")
	}
}

func TestWebLiveContextInspectionIncludesMemoryWithoutModelCall(t *testing.T) {
	e, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	provider := &prefixCaptureProvider{}
	e.prov = provider
	s := NewServer(e, false)
	inspect := func() agent.ContextReport {
		t.Helper()
		rec := httptest.NewRecorder()
		s.handleContext(rec, httptest.NewRequest(http.MethodGet, "/api/context", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("inspection status=%d: %s", rec.Code, rec.Body.String())
		}
		var payload struct {
			Report agent.ContextReport `json:"report"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Report
	}
	before := inspect()
	global, _ := e.webMemoryStores(e.Home())
	if global == nil {
		t.Fatal("fixture memory unavailable")
	}
	if err := global.Put(memory.Entry{ID: "inspection", Scope: memory.ScopePreference,
		Content: strings.Repeat("Fixture preference contains an important output constraint. ", 10), Source: memory.SourceUser}); err != nil {
		t.Fatal(err)
	}
	after := inspect()
	if after.RequestTokens-before.RequestTokens < 100 || len(provider.requests) != 0 {
		t.Fatalf("inspection omitted memory or called the model: before=%d after=%d calls=%d", before.RequestTokens, after.RequestTokens, len(provider.requests))
	}
}
