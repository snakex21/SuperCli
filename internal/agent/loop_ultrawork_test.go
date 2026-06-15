package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"supercli/internal/agent/ultrawork"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

// stubUltraworkGoal is a tiny GoalGate fake used by the
// loop integration tests. The perCall slice lets a test
// script a sequence of UnfinishedTasks values across the
// Sisyphus re-prompt dance (first call returns 2, second
// returns 0).
type stubUltraworkGoal struct {
	id      string
	title   string
	perCall []int
	mu      sync.Mutex
	calls   int
}

func (g *stubUltraworkGoal) ActiveID() string    { return g.id }
func (g *stubUltraworkGoal) ActiveTitle() string { return g.title }
func (g *stubUltraworkGoal) UnfinishedTasks(_ context.Context) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.calls >= len(g.perCall) {
		return 0
	}
	n := g.perCall[g.calls]
	g.calls++
	return n
}

type stubUltraworkCredit struct {
	hasBudget bool
	remSess   int64
	remDay    int64
}

func (c *stubUltraworkCredit) HasBudget() bool { return c.hasBudget }
func (c *stubUltraworkCredit) Remaining(_ context.Context) (int64, int64) {
	return c.remSess, c.remDay
}

// scriptedProvider emits a different script per call. The
// last script is replayed on overflow. (Loop integration
// tests usually only have 2-3 turns, so this is plenty.)
func scriptedProvider(name string, scripts [][]llm.Delta) *stubProvider {
	return &stubProvider{name: name, scripts: scripts}
}

// --- gate failure: no active goal ----------------------------------------

// When the user types "ultrawork ..." but there is no
// active /goal, the loop must surface that as a single
// ErrorEvent and close the channel. The model is never
// called.
func TestLoop_Ultrawork_GateFails_NoActiveGoal(t *testing.T) {
	p := scriptedProvider("echo", nil) // should never be invoked
	reg := tools.NewRegistry()
	loop, err := NewLoop(LoopConfig{
		Provider: p,
		Registry: reg,
		Ultrawork: &ultrawork.Wiring{
			Goal: &stubUltraworkGoal{id: "", title: ""},
		},
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	ch, err := loop.Run(context.Background(), "ultrawork migrate to v2")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var evs []Event
	for e := range ch {
		evs = append(evs, e)
	}
	if len(evs) != 1 {
		t.Fatalf("got %d events, want exactly 1 (gate-fail ErrorEvent); evs=%+v", len(evs), evs)
	}
	ee, ok := evs[0].(ErrorEvent)
	if !ok {
		t.Fatalf("first event = %T, want ErrorEvent", evs[0])
	}
	if !strings.Contains(ee.Err.Error(), "ultrawork gate failed") {
		t.Errorf("error = %v, want it to mention ultrawork gate", ee.Err)
	}
	if !strings.Contains(ee.Err.Error(), "no /goal active") {
		t.Errorf("error = %v, want it to mention /goal", ee.Err)
	}
	if p.calls != 0 {
		t.Errorf("provider was called %d times; want 0 (gate should short-circuit before any model call)", p.calls)
	}
}

// --- gate failure: credit budget exhausted -----------------------------

// When the credit gate is wired with a cap and Remaining
// is 0 on both axes, the loop must surface that.
func TestLoop_Ultrawork_GateFails_CreditsExhausted(t *testing.T) {
	p := scriptedProvider("echo", nil)
	reg := tools.NewRegistry()
	loop, err := NewLoop(LoopConfig{
		Provider: p,
		Registry: reg,
		Ultrawork: &ultrawork.Wiring{
			Goal:   &stubUltraworkGoal{id: "g1", title: "ship f9"},
			Credit: &stubUltraworkCredit{hasBudget: true, remSess: 0, remDay: 0},
		},
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	ch, _ := loop.Run(context.Background(), "ulw ship it")
	var evs []Event
	for e := range ch {
		evs = append(evs, e)
	}
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1; evs=%+v", len(evs), evs)
	}
	ee := evs[0].(ErrorEvent)
	if !strings.Contains(ee.Err.Error(), "out of credits") {
		t.Errorf("error = %v, want 'out of credits'", ee.Err)
	}
}

// --- happy path: Sisyphus re-prompts on unfinished tasks ---------------

// First model call: text "let me look at the tasks", no
// tool calls → Sisyphus fires (1 unfinished).
// Second model call: text "all done", no tool calls →
// UnfinishedTasks now returns 0, Sisyphus yields → DoneEvent.
func TestLoop_Ultrawork_SisyphusRePrompts(t *testing.T) {
	p := scriptedProvider("echo", [][]llm.Delta{
		// Call 1: model says something, no tool calls
		{{Content: "let me look at the tasks"}, {FinishReason: "stop", Usage: &llm.Usage{Input: 1, Output: 2, Total: 3}}},
		// Call 2: model says "done", no tool calls
		{{Content: "all done"}, {FinishReason: "stop", Usage: &llm.Usage{Input: 1, Output: 2, Total: 3}}},
	})
	reg := tools.NewRegistry()
	goalGate := &stubUltraworkGoal{
		id:      "g1",
		title:   "ship f9",
		perCall: []int{1, 0}, // 1 unfinished on first Sisyphus check, 0 on second
	}
	loop, err := NewLoop(LoopConfig{
		Provider: p,
		Registry: reg,
		Ultrawork: &ultrawork.Wiring{
			Goal:        goalGate,
			SisyphusMax: 3,
		},
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	ch, _ := loop.Run(context.Background(), "ultrawork ship f9")
	var evs []Event
	for e := range ch {
		evs = append(evs, e)
	}

	// Expected sequence:
	//   MessageEvent("let me look at the tasks")
	//   SisyphusEvent{Hit:1, Text:...}
	//   MessageEvent("all done")
	//   DoneEvent{...}
	if len(evs) != 4 {
		t.Fatalf("got %d events, want 4; evs=%+v", len(evs), evs)
	}
	if _, ok := evs[0].(MessageEvent); !ok {
		t.Errorf("evs[0] = %T, want MessageEvent", evs[0])
	}
	se, ok := evs[1].(SisyphusEvent)
	if !ok {
		t.Fatalf("evs[1] = %T, want SisyphusEvent", evs[1])
	}
	if se.Hit != 1 {
		t.Errorf("SisyphusEvent.Hit = %d, want 1", se.Hit)
	}
	if !strings.Contains(se.Text, "1 todo") {
		t.Errorf("SisyphusEvent.Text = %q, want it to mention '1 todo'", se.Text)
	}
	if !strings.Contains(se.Text, "Sisyphus @1/3") {
		t.Errorf("SisyphusEvent.Text = %q, want the attempt counter 'Sisyphus @1/3'", se.Text)
	}
	if _, ok := evs[2].(MessageEvent); !ok {
		t.Errorf("evs[2] = %T, want MessageEvent", evs[2])
	}
	if _, ok := evs[3].(DoneEvent); !ok {
		t.Errorf("evs[3] = %T, want DoneEvent", evs[3])
	}
	if p.calls != 2 {
		t.Errorf("provider was called %d times, want 2", p.calls)
	}

	// The system prompt section should have been injected
	// exactly once (at the start of Run). The Sisyphus
	// reminder should also be in Messages, as a system
	// message appended after the Sisyphus check.
	if !containsSystemText(loop.Messages, "ULTRAWORK MODE ACTIVE") {
		t.Error("Messages should contain the ULTRAWORK system-prompt section")
	}
	if !containsSystemText(loop.Messages, "Sisyphus @1/3") {
		t.Error("Messages should contain the Sisyphus reminder from the re-prompt")
	}
}

// --- Sisyphus caps re-prompts at MaxConsecutive ------------------------

// Model keeps emitting no tool calls while tasks remain
// unfinished. Sisyphus should re-prompt MaxConsecutive
// times (3) and then yield so the run finishes.
func TestLoop_Ultrawork_SisyphusCap(t *testing.T) {
	// 5 calls needed: 3 Sisyphus re-prompts + the final
	// call after the cap is hit.
	scripts := make([][]llm.Delta, 5)
	for i := range scripts {
		scripts[i] = []llm.Delta{
			{Content: "still working"},
			{FinishReason: "stop", Usage: &llm.Usage{Total: 1}},
		}
	}
	p := scriptedProvider("echo", scripts)
	goalGate := &stubUltraworkGoal{
		id:      "g1",
		title:   "loop",
		perCall: []int{3, 3, 3, 3, 3}, // always unfinished
	}
	loop, err := NewLoop(LoopConfig{
		Provider: p,
		Registry: tools.NewRegistry(),
		Ultrawork: &ultrawork.Wiring{
			Goal:        goalGate,
			SisyphusMax: 3,
		},
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	ch, _ := loop.Run(context.Background(), "ultrawork go")
	var sisyphusHits int
	var sawDone bool
	for e := range ch {
		if _, ok := e.(SisyphusEvent); ok {
			sisyphusHits++
		}
		if _, ok := e.(DoneEvent); ok {
			sawDone = true
		}
	}
	if sisyphusHits != 3 {
		t.Errorf("Sisyphus fired %d times, want 3 (MaxConsecutive)", sisyphusHits)
	}
	if !sawDone {
		t.Error("DoneEvent never emitted; Sisyphus should give up after cap and let the run finish")
	}
	if p.calls != 4 {
		t.Errorf("provider was called %d times, want 4 (1 initial + 3 re-prompts, then 4th call yields DoneEvent when cap is exceeded)", p.calls)
	}
}

// --- keyword not detected → no Sisyphus, normal flow -------------------

// When the user prompt does NOT contain the keyword, the
// loop must run normally even when Ultrawork wiring is
// present.
func TestLoop_Ultrawork_KeywordAbsent(t *testing.T) {
	p := scriptedProvider("echo", [][]llm.Delta{
		{{Content: "ok"}, {FinishReason: "stop"}},
	})
	// Goal gate is wired but the prompt does NOT contain
	// the keyword → no gate check, no Sisyphus.
	goalGate := &stubUltraworkGoal{id: "g1", perCall: []int{99}}
	loop, err := NewLoop(LoopConfig{
		Provider: p,
		Registry: tools.NewRegistry(),
		Ultrawork: &ultrawork.Wiring{
			Goal: goalGate,
		},
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	ch, _ := loop.Run(context.Background(), "please refactor this file")
	var sawSisyphus bool
	var sawDone bool
	for e := range ch {
		if _, ok := e.(SisyphusEvent); ok {
			sawSisyphus = true
		}
		if _, ok := e.(DoneEvent); ok {
			sawDone = true
		}
	}
	if sawSisyphus {
		t.Error("Sisyphus fired even though the keyword was absent")
	}
	if !sawDone {
		t.Error("DoneEvent missing")
	}
	if goalGate.calls != 0 {
		t.Errorf("UnfinishedTasks called %d times; want 0 (gate/Sisyphus should not engage)", goalGate.calls)
	}
}

// --- back-compat: Ultrawork nil → keyword ignored -----------------------

func TestLoop_Ultrawork_DisabledWiring(t *testing.T) {
	p := scriptedProvider("echo", [][]llm.Delta{
		{{Content: "ok"}, {FinishReason: "stop"}},
	})
	// Ultrawork is nil in the config. The keyword should
	// be ignored completely.
	loop, err := NewLoop(LoopConfig{
		Provider: p,
		Registry: tools.NewRegistry(),
		// Ultrawork not set
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	ch, _ := loop.Run(context.Background(), "ultrawork this should be ignored")
	var sawSisyphus bool
	var sawDone bool
	for e := range ch {
		if _, ok := e.(SisyphusEvent); ok {
			sawSisyphus = true
		}
		if _, ok := e.(DoneEvent); ok {
			sawDone = true
		}
	}
	if sawSisyphus {
		t.Error("Sisyphus should never fire when Ultrawork is nil")
	}
	if !sawDone {
		t.Error("DoneEvent missing")
	}
}

// --- Sisyphus fires across multiple turns -----------------------------

// 3 Sisyphus re-prompts in a row, then the model "finishes"
// the work. The Sisyphus hit counter must increment
// 1, 2, 3 across the three reminders.
func TestLoop_Ultrawork_SisyphusHitCounterIncrements(t *testing.T) {
	scripts := make([][]llm.Delta, 4)
	for i := range scripts {
		scripts[i] = []llm.Delta{
			{Content: "working"},
			{FinishReason: "stop", Usage: &llm.Usage{Total: 1}},
		}
	}
	p := scriptedProvider("echo", scripts)
	goalGate := &stubUltraworkGoal{
		id:      "g1",
		title:   "ship f9",
		perCall: []int{2, 2, 2, 0}, // 2 unfinished three times, then 0
	}
	loop, _ := NewLoop(LoopConfig{
		Provider: p,
		Registry: tools.NewRegistry(),
		Ultrawork: &ultrawork.Wiring{
			Goal:        goalGate,
			SisyphusMax: 5,
		},
	})
	ch, _ := loop.Run(context.Background(), "ultrawork ship")
	var hits []int
	for e := range ch {
		if se, ok := e.(SisyphusEvent); ok {
			hits = append(hits, se.Hit)
		}
	}
	if len(hits) != 3 {
		t.Fatalf("got %d Sisyphus hits, want 3; hits=%v", len(hits), hits)
	}
	for i, h := range hits {
		want := i + 1
		if h != want {
			t.Errorf("hits[%d] = %d, want %d", i, h, want)
		}
	}
}

// --- helpers ------------------------------------------------------------

func containsSystemText(msgs []llm.Message, needle string) bool {
	for _, m := range msgs {
		if m.Role == llm.RoleSystem && strings.Contains(m.Content, needle) {
			return true
		}
	}
	return false
}
