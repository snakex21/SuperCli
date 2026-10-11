package webgui

// Regression tests for the web GUI context-defense wiring. Before
// this wiring existed the web loop had no WindowFor and no
// Summarizer: every model was assumed to have a 16384-token window
// and auto-compaction always took the blind hide fallback, so the
// model silently lost the entire prior conversation (observed live:
// "straciłem kontekst poprzedniej rozmowy" after "[earlier context
// cleared — 35 message(s) compacted]").

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

// writeDataConfig drops a global config.toml into dataDir.
func writeDataConfig(t *testing.T, dataDir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dataDir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
}

// fatTurns fabricates prior conversation turns big enough to blow a
// tiny context window.
func fatTurns(n, chars int) []llm.Message {
	filler := strings.Repeat("kontekstowy balast projektu webgui ", chars/35+1)
	msgs := make([]llm.Message, 0, 2*n)
	for i := 0; i < n; i++ {
		msgs = append(msgs,
			llm.Message{Role: llm.RoleUser, Content: "pytanie: " + filler},
			llm.Message{Role: llm.RoleAssistant, Content: "odpowiedź: " + filler},
		)
	}
	return msgs
}

func assertWebCompactHelperCalls(t *testing.T, p *compactResolverProvider, wantMain int) {
	t.Helper()
	helpers := 0
	for _, request := range p.requests {
		if request.purpose != llm.PurposeCompact {
			continue
		}
		helpers++
		var body struct {
			Messages []llm.Message
			Tools    []llm.ToolDef
		}
		if err := json.Unmarshal(request.body, &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Messages) != 2 || body.Messages[0].Role != llm.RoleSystem || body.Messages[1].Role != llm.RoleUser || len(body.Tools) != 0 {
			t.Fatalf("helper request bypassed the engine summarizer: messages=%d tools=%d", len(body.Messages), len(body.Tools))
		}
	}
	if helpers != 1 || len(p.requests) != 1+wantMain {
		t.Fatalf("helper calls=%d total=%d, want exactly 1 helper and %d main calls", helpers, len(p.requests), wantMain)
	}
}

// TestWebLoop_AutoCompactUsesSummary: a web loop over a tiny window
// must compact through the summarizer (summary message injected, the
// fresh user turn kept verbatim), not the blind hide fallback.
func TestWebLoop_AutoCompactUsesSummary(t *testing.T) {
	dir := t.TempDir()
	writeDataConfig(t, dir, "context_window = 300\n")
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	// Keep the engine's real summarizer, provider resolver and compaction path.
	// The deterministic provider supplies a completed four-section summary;
	// an overlong echoed transcript is no longer silently cut into a success.
	p := &compactResolverProvider{t: t, model: eng.cfg.Model}
	eng.prov = p
	loop, err := eng.newLoopWithSession(fatTurns(4, 2000), nil)
	if err != nil {
		t.Fatalf("newLoopWithSession: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	events, err := loop.Run(ctx, "kontynuuj bieżące zadanie")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var compacted *agent.AutoCompactEvent
	for ev := range events {
		if failure, ok := ev.(agent.ErrorEvent); ok {
			t.Fatalf("automatic compaction or main completion failed: %v", failure.Err)
		}
		if e, ok := ev.(agent.AutoCompactEvent); ok {
			compacted = &e
		}
	}
	assertWebCompactHelperCalls(t, p, 1)
	if compacted == nil {
		t.Fatal("expected AutoCompactEvent over a 300-token window")
	}
	if compacted.Window != 300 {
		t.Errorf("compacted against window %d, want config context_window 300", compacted.Window)
	}

	// The summary path must have run: a user-role message carrying the
	// resume framing replaces the old turns (user role, not system —
	// strict chat templates reject mid-history system messages)...
	var summary string
	for _, m := range loop.AllMessages() {
		if agent.IsLegacyCompactionSummary(m) {
			summary = m.Content
		}
	}
	if summary == "" {
		t.Fatal("no compaction summary in history — blind hide fallback ran instead of the summarizer")
	}
	for _, section := range []string{"Goal: finish the coding task.", "Done: inspected the old source.", "State: keep portable data and every user constraint.", "Pending: finish the current task."} {
		if !strings.Contains(summary, section) {
			t.Fatalf("completed summary lost section %q", section)
		}
	}
	// ...and the current user task survives verbatim next to it.
	found := false
	for _, m := range loop.AllMessages() {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, "kontynuuj bieżące zadanie") {
			found = true
		}
	}
	if !found {
		t.Error("current user task lost during compaction")
	}
}

// TestWebLoop_WindowFromConfig: config context_window must reach the
// web loop (it previously never did — the loop always assumed 16384).
func TestWebLoop_WindowFromConfig(t *testing.T) {
	dir := t.TempDir()
	writeDataConfig(t, dir, "context_window = 99999\n")
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	loop, err := eng.newLoop()
	if err != nil {
		t.Fatalf("newLoop: %v", err)
	}
	if got := loop.ContextWindow(); got != 99999 {
		t.Errorf("ContextWindow = %d, want 99999 from config", got)
	}
}

// TestWebLoop_WindowFromLearnedLimit: a context limit learned from a
// past provider error must size the web loop's window too.
func TestWebLoop_WindowFromLearnedLimit(t *testing.T) {
	dir := t.TempDir()
	llm.LoadLearnedLimits(dir).Learn("echo-test", 4242)
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	loop, err := eng.newLoop()
	if err != nil {
		t.Fatalf("newLoop: %v", err)
	}
	if got := loop.ContextWindow(); got != 4242 {
		t.Errorf("ContextWindow = %d, want learned 4242", got)
	}
}

func TestContextCompactEndpointPreservesTranscriptAndRewritesProjection(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	p := &compactResolverProvider{t: t, model: eng.cfg.Model}
	eng.prov = p
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(dir, "echo-test", "manual compact")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "old question " + strings.Repeat("x", 20000)},
		{Role: llm.RoleAssistant, Content: "old answer"},
		{Role: llm.RoleUser, Content: "latest question"},
		{Role: llm.RoleAssistant, Content: "latest answer"},
	}
	for _, msg := range msgs {
		if err := writer.AppendMessage(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
	}
	archiveBefore, err := store.ReadMessages(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServer(eng, false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/context/compact", strings.NewReader(`{"session_id":"`+sess.ID+`"}`))
	srv.handleContextCompact(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertWebCompactHelperCalls(t, p, 0)
	var response contextCompactResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.Removed == 0 || response.After >= response.Before {
		t.Fatalf("unexpected response: %+v", response)
	}
	rows, err := store.ReadMessages(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(msgs) { // summary belongs only to the model projection
		t.Fatalf("transcript rows=%d, want original %d", len(rows), len(msgs))
	}
	if !reflect.DeepEqual(rows, archiveBefore) {
		t.Fatal("successful compaction changed the original transcript bytes or metadata")
	}
	projection, err := store.ReadModelContext(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection) >= len(msgs) {
		t.Fatalf("projection was not compacted: %d messages", len(projection))
	}
	foundSummary := false
	for _, msg := range projection {
		if agent.IsLegacyCompactionSummary(msg) {
			foundSummary = true
			for _, section := range []string{"Goal: finish the coding task.", "Done: inspected the old source.", "State: keep portable data and every user constraint.", "Pending: finish the current task."} {
				if !strings.Contains(msg.Content, section) {
					t.Fatalf("completed projected summary lost section %q", section)
				}
			}
		}
	}
	if !foundSummary {
		t.Fatal("compacted projection is missing the summary hand-off")
	}
}

type webCompactFailureProvider struct {
	t      *testing.T
	mode   string
	cancel context.CancelFunc
	calls  int
}

func (p *webCompactFailureProvider) Name() string { return "echo-test" }

func (p *webCompactFailureProvider) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	if llm.PurposeFromContext(ctx) != llm.PurposeCompact || len(messages) != 2 || messages[0].Role != llm.RoleSystem || messages[1].Role != llm.RoleUser || len(defs) != 0 {
		p.t.Error("failure fixture did not reach the real engine helper request")
	}
	if len(messages) == 2 && (!strings.Contains(messages[1].Content, "prior instruction: keep all user constraints") || !strings.Contains(messages[1].Content, "new investigation evidence") || strings.Contains(messages[1].Content, "latest question")) {
		p.t.Error("helper lost saved-summary/new evidence or included the protected recent tail")
	}
	switch p.mode {
	case "transport":
		return nil, errors.New("deterministic helper transport failure")
	case "cancel":
		p.cancel()
		return nil, ctx.Err()
	}
	stream := make(chan llm.Delta, 1)
	stream <- llm.Delta{
		Content: "Goal: preserve the coding task.\nDone: " + strings.Repeat("completed evidence ", 300) +
			"\nState: the only earlier prohibition must survive.\nPending: finish the remaining check.",
		FinishReason: "stop",
		Usage:        &llm.Usage{Input: 120, Output: 1500},
	}
	close(stream)
	return stream, nil
}

func TestContextCompactEndpointFailurePreservesSavedProjectionAndResume(t *testing.T) {
	for _, mode := range []struct {
		name      string
		status    int
		errorText string
	}{
		{"overlong", http.StatusBadGateway, "summary exceeds byte limit"},
		{"transport", http.StatusBadGateway, "deterministic helper transport failure"},
		{"cancel", 499, "context canceled"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			dir := t.TempDir()
			writeDataConfig(t, dir, "orchestrator = false\npreflight_repo = false\ncontext_window = 100000\n")
			eng, err := NewEngine(echoConfig(), dir, dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = eng.Close() })
			store, err := eng.sessionStore()
			if err != nil {
				t.Fatal(err)
			}
			sess, err := store.Create(dir, "echo-test", "failed compact preserves existing projection")
			if err != nil {
				t.Fatal(err)
			}
			writer := session.NewWriter(store, sess.ID)
			archive := []llm.Message{
				{Role: llm.RoleUser, Content: "prior instruction: keep all user constraints " + strings.Repeat("original evidence ", 1500)},
				{Role: llm.RoleAssistant, Content: "old completed investigation"},
				{Role: llm.RoleUser, Content: "new investigation evidence " + strings.Repeat("new source facts ", 1500)},
				{Role: llm.RoleAssistant, Content: "retain the observed failure and unfinished task"},
				{Role: llm.RoleUser, Content: "previous correction: preserve Żółć and spaces"},
				{Role: llm.RoleAssistant, Content: "the correction remains applicable"},
				{Role: llm.RoleUser, Content: "latest question"},
				{Role: llm.RoleAssistant, Content: "latest answer"},
			}
			for _, message := range archive {
				if err := writer.AppendMessage(context.Background(), message); err != nil {
					t.Fatal(err)
				}
			}
			projection := append([]llm.Message{{Role: llm.RoleUser, Content: agent.WrapCompactSummary("Goal: continue the coding task.\nDone: old completed investigation.\nState: prior instruction: keep all user constraints.\nPending: resume new evidence.")}}, archive[2:]...)
			if err := store.SaveContextProjection(context.Background(), sess.ID, projection); err != nil {
				t.Fatal(err)
			}
			archiveBefore, err := store.ReadMessages(context.Background(), sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			projectionBefore, err := store.ReadModelContext(context.Background(), sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(projectionBefore, projection) {
				t.Fatal("fixture did not persist its earlier summary and subsequent evidence")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := &webCompactFailureProvider{t: t, mode: mode.name, cancel: cancel}
			eng.prov = p
			srv := NewServer(eng, false)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/context/compact", strings.NewReader(`{"session_id":"`+sess.ID+`"}`)).WithContext(ctx)
			srv.handleContextCompact(rec, req)
			if rec.Code != mode.status || !strings.Contains(rec.Body.String(), mode.errorText) {
				t.Fatalf("failed helper status=%d body=%s, want %d/%q", rec.Code, rec.Body.String(), mode.status, mode.errorText)
			}
			if p.calls != 1 {
				t.Fatalf("failed default helper calls=%d, want exactly 1 with no retry or main call", p.calls)
			}
			archiveAfter, err := store.ReadMessages(context.Background(), sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			projectionAfter, err := store.ReadModelContext(context.Background(), sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(archiveAfter, archiveBefore) || !reflect.DeepEqual(projectionAfter, projectionBefore) {
				t.Fatal("rejected helper changed the raw transcript or existing model projection")
			}
			resumed, err := eng.newLoopWithSession(projectionAfter, nil)
			if err != nil {
				t.Fatal(err)
			}
			body := resumed.AllMessages()
			for len(body) > 0 && body[0].Role == llm.RoleSystem {
				body = body[1:]
			}
			if !reflect.DeepEqual(body, projectionBefore) || p.calls != 1 {
				t.Fatal("resume lost unchanged projected facts/tail or performed an extra helper call")
			}
		})
	}
}
