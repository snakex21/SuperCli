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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
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
	loop, err := eng.newLoopWithSession(fatTurns(4, 700), nil)
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
		if e, ok := ev.(agent.AutoCompactEvent); ok {
			compacted = &e
		}
	}
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
		if m.Role == llm.RoleUser && strings.Contains(m.Content, "continued from a previous conversation") {
			summary = m.Content
		}
	}
	if summary == "" {
		t.Fatal("no compaction summary in history — blind hide fallback ran instead of the summarizer")
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
	loop, err := eng.newLoop()
	if err != nil {
		t.Fatalf("newLoop: %v", err)
	}
	if got := loop.ContextWindow(); got != 4242 {
		t.Errorf("ContextWindow = %d, want learned 4242", got)
	}
}
