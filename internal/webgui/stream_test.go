package webgui

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
)

func TestShouldAttachPreflightWaitsForFirstProjectTurn(t *testing.T) {
	if shouldAttachPreflight(nil, "hello") {
		t.Fatal("greeting should not pay repository preflight")
	}
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}
	if !shouldAttachPreflight(history, "inspect project files") {
		t.Fatal("first project turn should receive repository preflight")
	}
	history = append(history,
		llm.Message{Role: llm.RoleUser, Content: "inspect project files"},
		llm.Message{Role: llm.RoleAssistant, Content: "done"},
	)
	if shouldAttachPreflight(history, "fix another file") {
		t.Fatal("preflight must not repeat after a project turn")
	}
}

type stalledWebProvider struct{}

func (stalledWebProvider) Name() string { return "stalled" }

func (stalledWebProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

type failAfterToolWebProvider struct{ calls int }

func (p *failAfterToolWebProvider) Name() string { return "fail-after-tool" }

func (p *failAfterToolWebProvider) Complete(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	ch := make(chan llm.Delta, 3)
	if p.calls == 1 {
		ch <- llm.Delta{ToolCall: &llm.ToolCall{ID: "list", Name: "list_dir", Arguments: `{"path":""}`}}
		ch <- llm.Delta{FinishReason: "tool_calls", Usage: &llm.Usage{Input: 10, Output: 2, Total: 12}}
	} else {
		ch <- llm.Delta{Err: errors.New("upstream failed after tool")}
	}
	close(ch)
	return ch, nil
}

func TestToWireEvent_Message(t *testing.T) {
	w, keep := toWireEvent(agent.MessageEvent{Text: "hello"})
	if !keep {
		t.Fatal("message event should be kept")
	}
	if w.Type != "message" || w.Text != "hello" {
		t.Errorf("got %+v", w)
	}
}

func TestToWireEvent_ReasoningUsesDedicatedChannel(t *testing.T) {
	w, keep := toWireEvent(agent.ReasoningEvent{Text: "plan"})
	if !keep || w.Type != "reasoning" || w.Text != "plan" {
		t.Fatalf("wire reasoning = %+v keep=%v", w, keep)
	}
}

func TestToWireEvent_ToolCall(t *testing.T) {
	w, keep := toWireEvent(agent.ToolCallEvent{Name: "write_file", Args: `{"path":"x"}`, ID: "t1"})
	if !keep {
		t.Fatal("tool_call should be kept")
	}
	if w.Type != "tool_call" || w.Name != "write_file" || w.ID != "t1" {
		t.Errorf("got %+v", w)
	}
}

func TestToWireEvent_ToolResultWithError(t *testing.T) {
	w, _ := toWireEvent(agent.ToolResultEvent{ID: "t1", Err: errors.New("boom")})
	if w.Type != "tool_result" || w.Err != "boom" {
		t.Errorf("got %+v", w)
	}
}

func TestToWireEvent_ToolResultSuccess(t *testing.T) {
	w, _ := toWireEvent(agent.ToolResultEvent{ID: "t1", Output: "ok"})
	if w.Err != "" || w.Output != "ok" {
		t.Errorf("expected no error, got %+v", w)
	}
}

func TestToWireEvent_Done(t *testing.T) {
	w, keep := toWireEvent(agent.DoneEvent{Usage: agent.Usage{Input: 10, Output: 5, Total: 15}})
	if !keep || w.Type != "done" {
		t.Fatalf("got %+v keep=%v", w, keep)
	}
	if w.TokIn != 10 || w.TokOut != 5 || w.TokTotal != 15 {
		t.Errorf("usage mismatch: %+v", w)
	}
	// No cache/reasoning reported → both omitted.
	if w.CacheHitPct != 0 || w.ReasoningTok != 0 {
		t.Errorf("expected zero observability, got %+v", w)
	}
}

func TestToWireEvent_DoneObservability(t *testing.T) {
	w, _ := toWireEvent(agent.DoneEvent{Usage: agent.Usage{Input: 2000, Output: 100, Total: 2100, Cached: 1500, Reasoning: 80}})
	if w.CacheHitPct != 75 {
		t.Errorf("cache-hit%% = %d, want 75", w.CacheHitPct)
	}
	if w.TokCached != 1500 {
		t.Errorf("tok_cached = %d, want 1500", w.TokCached)
	}
	if w.ReasoningTok != 80 {
		t.Errorf("reasoning = %d, want 80", w.ReasoningTok)
	}
}

func TestToWireEvent_Worker(t *testing.T) {
	w, keep := toWireEvent(agent.WorkerNotificationEvent{TaskID: "t1", Agent: "general", Status: "done", Summary: "did it", Text: "full report"})
	if !keep || w.Type != "worker" {
		t.Fatalf("got %+v keep=%v", w, keep)
	}
	if w.ID != "t1" || w.Name != "general" || w.Status != "done" || w.Output != "did it" || w.Text != "full report" {
		t.Errorf("worker fields: %+v", w)
	}
}

func TestToWireEvent_WorkerProgress(t *testing.T) {
	w, keep := toWireEvent(agent.WorkerProgressEvent{
		TaskID: "worker-1", Agent: "explore", Kind: "tool_call", CallID: "call-2",
		Tool: "search_code", Args: `{"query":"worker"}`,
	})
	if !keep || w.Type != "worker_progress" {
		t.Fatalf("got %+v keep=%v", w, keep)
	}
	if w.ID != "worker-1" || w.Name != "explore" || w.Kind != "tool_call" ||
		w.CallID != "call-2" || w.Tool != "search_code" {
		t.Errorf("worker progress fields: %+v", w)
	}
}

func TestToWireEvent_DraftUsed(t *testing.T) {
	w, keep := toWireEvent(agent.DraftUsedEvent{Decision: "accepted", DraftModel: "small", VerifierModel: "big", Savings: 42})
	if !keep || w.Type != "notice" {
		t.Fatalf("got %+v keep=%v", w, keep)
	}
	for _, want := range []string{"accepted", "small", "big", "42"} {
		if !strings.Contains(w.Text, want) {
			t.Errorf("draft notice missing %q: %q", want, w.Text)
		}
	}
}

func TestToWireEvent_MessagesHidden(t *testing.T) {
	w, keep := toWireEvent(agent.MessagesHiddenEvent{Count: 3, Reason: "budget"})
	if !keep || w.Type != "notice" || !strings.Contains(w.Text, "3") || !strings.Contains(w.Text, "budget") {
		t.Errorf("got %+v keep=%v", w, keep)
	}
}

func TestToWireEvent_Error(t *testing.T) {
	w, _ := toWireEvent(agent.ErrorEvent{Err: errors.New("failed")})
	if w.Type != "error" || w.Err != "failed" {
		t.Errorf("got %+v", w)
	}
}

func TestToWireEvent_ErrorNil(t *testing.T) {
	// Defensive: an ErrorEvent with a nil Err must not panic.
	w, _ := toWireEvent(agent.ErrorEvent{})
	if w.Type != "error" || w.Err != "" {
		t.Errorf("got %+v", w)
	}
}

func TestToWireEvent_Reflection(t *testing.T) {
	w, keep := toWireEvent(agent.ReflectionEvent{Step: 3, Text: "thinking"})
	if !keep || w.Type != "reflection" || w.Step != 3 {
		t.Errorf("got %+v keep=%v", w, keep)
	}
}

func TestToWireEvent_Compact(t *testing.T) {
	w, keep := toWireEvent(agent.AutoCompactEvent{Reason: "auto", Removed: 4, Window: 16384})
	if !keep || w.Type != "compact" {
		t.Fatalf("got %+v", w)
	}
	if !strings.Contains(w.Text, "auto") || !strings.Contains(w.Text, "4") {
		t.Errorf("compact text missing detail: %q", w.Text)
	}
}

func TestWireEvent_Marshal(t *testing.T) {
	w := wireEvent{Type: "message", Text: "hi"}
	b := w.marshal()
	s := string(b)
	if !strings.Contains(s, `"type":"message"`) || !strings.Contains(s, `"text":"hi"`) {
		t.Errorf("marshal output: %s", s)
	}
	// Omitempty: a bare message must not carry tool/usage fields.
	if strings.Contains(s, "tok_total") || strings.Contains(s, `"name"`) {
		t.Errorf("expected omitted empty fields, got %s", s)
	}
}

func TestMessageCoalescerPreservesSemanticBoundaries(t *testing.T) {
	var got []wireEvent
	c := messageCoalescer{emit: func(ev wireEvent) { got = append(got, ev) }}
	if !c.Push(wireEvent{Type: "message", Text: "one"}) {
		t.Fatal("first text chunk did not start a batch")
	}
	if c.Push(wireEvent{Type: "message", Text: " two"}) {
		t.Fatal("second text chunk started a second batch")
	}
	if len(got) != 0 {
		t.Fatalf("text flushed too early: %+v", got)
	}
	if !c.Push(wireEvent{Type: "reasoning", Text: "why"}) {
		t.Fatal("first reasoning chunk did not start its own batch")
	}
	if c.Push(wireEvent{Type: "reasoning", Text: " now"}) {
		t.Fatal("second reasoning chunk started a second batch")
	}
	c.Push(wireEvent{Type: "tool_call", Name: "search_code"})
	if len(got) != 3 || got[0].Type != "message" || got[0].Text != "one two" ||
		got[1].Type != "reasoning" || got[1].Text != "why now" || got[2].Type != "tool_call" {
		t.Fatalf("event order changed: %+v", got)
	}
}

func TestReasoningFallbackSentenceLivesInTheUILanguageCatalog(t *testing.T) {
	// The Go layer has no string catalog, so it must never build this
	// sentence: it announces the token count and the front-end localizes it.
	// Guard both halves of that contract.
	for _, name := range []string{"stream.go", "stream_run.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(src), "tokens, but the provider") || strings.Contains(string(src), "tokenow rozumowania") {
			t.Errorf("%s builds the reasoning fallback sentence in Go; it belongs in assets/js/01-i18n.js", name)
		}
	}
	dict, err := assetsFS.ReadFile("assets/js/01-i18n.js")
	if err != nil {
		t.Fatalf("read i18n catalog: %v", err)
	}
	if got := strings.Count(string(dict), `"reasoning.noSummary"`); got != 2 {
		t.Errorf("reasoning.noSummary defined %d times, want 2 (en and pl)", got)
	}
	if !strings.Contains(string(dict), "{n} reasoning tokens") {
		t.Error("English reasoning.noSummary must interpolate the token count as {n}")
	}
}

func TestRunStreamStopsAfterSemanticProgressTimeout(t *testing.T) {
	srv := newTestServer(t, false)
	id := seedWebSession(t, srv, srv.eng.Home(), "Existing")
	srv.eng.mu.Lock()
	srv.eng.prov = stalledWebProvider{}
	srv.eng.cfg.Timeout = 40 * time.Millisecond
	srv.eng.mu.Unlock()

	started := time.Now()
	err := srv.eng.runStream(context.Background(), "hello", id, "", func(wireEvent) {})
	if err == nil || !strings.Contains(err.Error(), "no model progress") {
		t.Fatalf("runStream error = %v, want semantic progress timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("progress timeout took too long: %s", elapsed)
	}
}

func TestRunStreamPersistsTelemetryForFailedTurn(t *testing.T) {
	srv := newTestServer(t, false)
	srv.eng.mu.Lock()
	srv.eng.prov = &failAfterToolWebProvider{}
	srv.eng.mu.Unlock()

	sessionID := ""
	err := srv.eng.runStream(context.Background(), "inspect project files", "", "", func(ev wireEvent) {
		if ev.Type == "session" {
			sessionID = ev.SessionID
		}
	})
	if err != nil {
		t.Fatalf("runStream: %v", err)
	}
	if sessionID == "" {
		t.Fatal("missing session id")
	}
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	turns, err := store.ReadTurnSummaries(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("failed turn telemetry = %+v", turns)
	}
	if turns[0].Steps != 2 || turns[0].Input != 10 || turns[0].Output != 2 || turns[0].ToolCalls != 1 {
		t.Fatalf("failed turn summary = %+v", turns[0])
	}
}

// diagProbeProvider reproduces the exact failure mix that made the
// original loops invisible in telemetry: a no-match search, a broken
// tool call, then an upstream error.
type diagProbeProvider struct{ calls int }

func (p *diagProbeProvider) Name() string { return "diag-probe" }

func (p *diagProbeProvider) Complete(_ context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	ch := make(chan llm.Delta, 4)
	switch p.calls {
	case 1:
		ch <- llm.Delta{ToolCall: &llm.ToolCall{ID: "s1", Name: "search_code", Arguments: `{"query":"zz9_not_here_42_","max":5}`}}
		ch <- llm.Delta{FinishReason: "tool_calls"}
	case 2:
		ch <- llm.Delta{ToolCall: &llm.ToolCall{ID: "r1", Name: "read_lines", Arguments: `{"path":"C:\\definitely\\not\\here\\ghost.md","start":1,"end":5}`}}
		ch <- llm.Delta{FinishReason: "tool_calls"}
	default:
		ch <- llm.Delta{Err: errors.New("boom after probes")}
	}
	close(ch)
	return ch, nil
}

// TestRunStreamPersistsToolDiag is the end-to-end guard for the
// tool_diag_json column: a run that no-match-searches, breaks a tool
// call and then dies must leave a signature in the DB — without it,
// every such run looked identical to a healthy one and the loops
// could only be found by dumping transcripts.
func TestRunStreamPersistsToolDiag(t *testing.T) {
	srv := newTestServer(t, false)
	srv.eng.mu.Lock()
	srv.eng.prov = &diagProbeProvider{}
	srv.eng.mu.Unlock()

	sessionID := ""
	err := srv.eng.runStream(context.Background(), "probe the project", "", "", func(ev wireEvent) {
		if ev.Type == "session" {
			sessionID = ev.SessionID
		}
	})
	// Provider errors surface as ErrorEvent (channel close returns nil);
	// the terminal kind is what must land in the diag, not the return value.
	if err != nil {
		t.Fatalf("runStream: %v", err)
	}
	if sessionID == "" {
		t.Fatal("missing session id")
	}
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	turns, err := store.ReadTurnSummaries(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns = %+v", turns)
	}
	diag := turns[0].ToolDiag
	if diag.NoOpSearches != 1 {
		t.Errorf("noop searches = %d, want 1 (diag=%+v)", diag.NoOpSearches, diag)
	}
	if diag.Failures["read_lines"] != 1 {
		t.Errorf("read_lines failures = %d, want 1 (diag=%+v)", diag.Failures["read_lines"], diag)
	}
	if diag.Messages["read_lines"] == "" {
		t.Errorf("read_lines failure message missing (diag=%+v)", diag)
	}
	if !strings.Contains(diag.Terminal, "boom after probes") {
		t.Errorf("terminal = %q, want boom after probes", diag.Terminal)
	}
}
