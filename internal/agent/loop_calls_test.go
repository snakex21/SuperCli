package agent

// Model-call ledger tests (trzeci pakiet, etap 2): every model
// call carries a purpose label, and model-powered aux operations
// (reflection, compact summary, draft) are booked as their own
// model:<purpose> phases instead of inflating the CLI phases
// (context_prepare / next_turn_prepare).

import (
	"context"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

// meteredStatsProvider wraps a stub with llm.Metered feeding the
// given recorder, mirroring the main.go wiring.
func meteredStatsProvider(p llm.Provider, rec stats.Recorder) llm.Provider {
	return llm.Metered(p, "test", llm.PurposeMain, func(s llm.CallStat) {
		rec.RecordCall(stats.Call{
			Purpose:    s.Purpose,
			Model:      s.Model,
			Provider:   s.Provider,
			Background: s.Background,
			Canceled:   s.Canceled,
			Failed:     s.Failed,
			TTFTUs:     s.TTFT.Microseconds(),
			DurationUs: s.Duration.Microseconds(),
			TokensIn:   s.TokensIn,
			TokensOut:  s.TokensOut,
			StartedAt:  s.StartedAt,
		})
	})
}

func callsByPurpose(calls []stats.Call) map[string][]stats.Call {
	out := map[string][]stats.Call{}
	for _, c := range calls {
		out[c.Purpose] = append(out[c.Purpose], c)
	}
	return out
}

// TestLoop_Calls_MainPurpose proves the plain coordinator step call
// is recorded with purpose "main" and the step number stamped.
func TestLoop_Calls_MainPurpose(t *testing.T) {
	p := &stubProvider{
		name: "m",
		scripts: [][]llm.Delta{{
			{Role: llm.RoleAssistant, Content: "hello"},
			{FinishReason: "stop", Usage: &llm.Usage{Input: 7, Output: 3, Total: 10}},
		}},
	}
	rec := stats.NewMemory()
	l := makeStatsLoop(t, meteredStatsProvider(p, rec), echoToolRegistry(t), rec)
	ch, err := l.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	drainEvents(t, ch)

	by := callsByPurpose(rec.Calls())
	main := by[llm.PurposeMain]
	if len(main) != 1 {
		t.Fatalf("main calls = %d, want 1 (all: %+v)", len(main), rec.Calls())
	}
	c := main[0]
	if c.TokensIn != 7 || c.TokensOut != 3 {
		t.Errorf("tokens = %d/%d, want 7/3", c.TokensIn, c.TokensOut)
	}
	if c.Step != 1 {
		t.Errorf("Step = %d, want 1", c.Step)
	}
	if c.Background {
		t.Error("main step call must be foreground")
	}
}

// TestLoop_Calls_NavigatorPurpose proves the pre-step navigator
// classification — which runs BEFORE the first stats turn opens and
// used to be invisible — is recorded with purpose "navigator".
func TestLoop_Calls_NavigatorPurpose(t *testing.T) {
	p := &stubProvider{
		name: "m",
		scripts: [][]llm.Delta{
			{ // call 1: navigator classification
				{Role: llm.RoleAssistant, Content: `{"mode":"coordinator"}`},
				{FinishReason: "stop"},
			},
			{ // call 2: the main step
				{Role: llm.RoleAssistant, Content: "done"},
				{FinishReason: "stop", Usage: &llm.Usage{Input: 1, Output: 1, Total: 2}},
			},
		},
	}
	rec := stats.NewMemory()
	l, err := NewLoop(LoopConfig{
		Provider:        meteredStatsProvider(p, rec),
		Registry:        echoToolRegistry(t),
		MaxSteps:        3,
		Stats:           rec,
		EnableNavigator: true, // no NavigatorAuto: every Run pays the model navigator
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	ch, err := l.Run(context.Background(), "please refactor the parser and run the tests")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	drainEvents(t, ch)

	by := callsByPurpose(rec.Calls())
	if len(by[llm.PurposeNavigator]) != 1 {
		t.Fatalf("navigator calls = %d, want 1 (all: %+v)", len(by[llm.PurposeNavigator]), rec.Calls())
	}
	if len(by[llm.PurposeMain]) != 1 {
		t.Fatalf("main calls = %d, want 1 (all: %+v)", len(by[llm.PurposeMain]), rec.Calls())
	}
	if s := by[llm.PurposeNavigator][0].Step; s != 0 {
		t.Errorf("navigator Step = %d, want 0 (outside any step)", s)
	}
}

// slowReflector simulates a reflection model call by sleeping.
type slowReflector struct {
	d    time.Duration
	text string
}

func (r *slowReflector) Reflect(ctx context.Context, history []llm.Message) (string, error) {
	time.Sleep(r.d)
	return r.text, nil
}

// TestLoop_Calls_ReflectionNotInNextTurnPrepare proves a slow
// reflection is booked as model:reflection and does NOT inflate the
// next_turn_prepare remainder (the audit found a 13.9s model call
// hidden there).
func TestLoop_Calls_ReflectionNotInNextTurnPrepare(t *testing.T) {
	const reflSleep = 60 * time.Millisecond
	p := &stubProvider{
		name: "m",
		scripts: [][]llm.Delta{
			{ // step 1: one tool call, so reflection (every 1 step) fires
				echoCall("c1"),
				{FinishReason: "tool_calls", Usage: &llm.Usage{Input: 1, Output: 1, Total: 2}},
			},
			{ // step 2: done
				{Role: llm.RoleAssistant, Content: "done"},
				{FinishReason: "stop", Usage: &llm.Usage{Input: 1, Output: 1, Total: 2}},
			},
		},
	}
	rec := stats.NewMemory()
	l, err := NewLoop(LoopConfig{
		Provider:     p,
		Registry:     echoToolRegistry(t),
		MaxSteps:     5,
		Stats:        rec,
		Reflector:    &slowReflector{d: reflSleep, text: "keep going"},
		ReflectEvery: 1,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	ch, err := l.Run(context.Background(), "use tools")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	drainEvents(t, ch)

	turns := rec.Snapshot()
	if len(turns) < 1 {
		t.Fatal("no turns recorded")
	}
	step1 := turns[0]
	refl := step1.Phases["model:"+llm.PurposeReflect]
	if refl < (reflSleep - 10*time.Millisecond).Microseconds() {
		t.Errorf("model:reflection = %dµs, want >= ~%v", refl, reflSleep)
	}
	if rest := step1.Phases[stats.PhaseNextTurnPrepare]; rest >= (reflSleep / 2).Microseconds() {
		t.Errorf("next_turn_prepare = %dµs — the reflection sleep leaked into the CLI remainder", rest)
	}
}

// TestLoop_Calls_CompactNotInContextPrepare proves the auto-compact
// summary call is booked as model:compact and excluded from
// context_prepare (which must measure pure CLI overhead).
func TestLoop_Calls_CompactNotInContextPrepare(t *testing.T) {
	const sumSleep = 60 * time.Millisecond
	p := &stubProvider{
		name: "m",
		scripts: [][]llm.Delta{{
			{Role: llm.RoleAssistant, Content: "done"},
			{FinishReason: "stop", Usage: &llm.Usage{Input: 1, Output: 1, Total: 2}},
		}},
	}
	rec := stats.NewMemory()
	l, err := NewLoop(LoopConfig{
		Provider: p,
		Registry: echoToolRegistry(t),
		MaxSteps: 3,
		Stats:    rec,
		// A tiny window forces the completed old turn to compact before the
		// new active user turn. Auto-compaction no longer summarizes the
		// currently running turn itself.
		WindowFor: func(string) int { return 100 },
		InitialMessages: []llm.Message{
			{Role: llm.RoleUser, Content: strings.Repeat("old context ", 300)},
			{Role: llm.RoleAssistant, Content: "old answer"},
		},
		Summarizer: func(ctx context.Context, prov llm.Provider, msgs []llm.Message) (string, error) {
			if got := llm.PurposeFromContext(ctx); got != llm.PurposeCompact {
				t.Errorf("summarizer ctx purpose = %q, want %q", got, llm.PurposeCompact)
			}
			time.Sleep(sumSleep)
			return "summary", nil
		},
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	ch, err := l.Run(context.Background(), "hello there, this is a prompt long enough to exceed one token")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	drainEvents(t, ch)

	turns := rec.Snapshot()
	if len(turns) < 1 {
		t.Fatal("no turns recorded")
	}
	step1 := turns[0]
	comp := step1.Phases["model:"+llm.PurposeCompact]
	if comp < (sumSleep - 10*time.Millisecond).Microseconds() {
		t.Errorf("model:compact = %dµs, want >= ~%v (phases: %v)", comp, sumSleep, step1.Phases)
	}
	if prep := step1.Phases[stats.PhaseContextPrepare]; prep >= (sumSleep / 2).Microseconds() {
		t.Errorf("context_prepare = %dµs — the summary sleep leaked into the CLI phase", prep)
	}
}

// TestLoop_Calls_NilStatsNoPanic proves the aux-phase bookkeeping is
// nil-safe: reflection + forced compaction with Stats == nil.
func TestLoop_Calls_NilStatsNoPanic(t *testing.T) {
	p := &stubProvider{
		name: "m",
		scripts: [][]llm.Delta{
			{
				echoCall("c1"),
				{FinishReason: "tool_calls"},
			},
			{
				{Role: llm.RoleAssistant, Content: "done"},
				{FinishReason: "stop"},
			},
		},
	}
	l, err := NewLoop(LoopConfig{
		Provider:     p,
		Registry:     echoToolRegistry(t),
		MaxSteps:     5,
		Reflector:    &slowReflector{d: time.Millisecond, text: "ok"},
		ReflectEvery: 1,
		WindowFor:    func(string) int { return 1 },
		Summarizer: func(ctx context.Context, prov llm.Provider, msgs []llm.Message) (string, error) {
			return "summary", nil
		},
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	ch, err := l.Run(context.Background(), "use tools")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for ev := range ch {
		if e, ok := ev.(ErrorEvent); ok {
			t.Fatalf("unexpected error event: %v", e.Err)
		}
	}
}
