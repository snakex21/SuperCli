package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/llm/draft"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
)

// draftStubProvider is a minimal Provider that returns
// a fixed draft plan text on every call. Used as the
// DRAFT provider (separate from the verifier's
// stubProvider).
type draftStubProvider struct {
	name string
	text string
}

func (p *draftStubProvider) Name() string { return p.name }

func (p *draftStubProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta, 3)
	go func() {
		defer close(ch)
		ch <- llm.Delta{Role: llm.RoleAssistant, Content: p.text}
		ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Output: 10}}
	}()
	return ch, nil
}

// draftRecordingSink is an override sink that
// records every call so tests can assert the loop
// reported an override.
type draftRecordingSink struct {
	mu      sync.Mutex
	records []DraftOverride
}

func (s *draftRecordingSink) RecordDraftOverride(_ context.Context, ev DraftOverride) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, ev)
	return nil
}

func (s *draftRecordingSink) snapshot() []DraftOverride {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DraftOverride, len(s.records))
	copy(out, s.records)
	return out
}

// TestLoop_DraftDisabledWhenConfigNil — the F11
// rule "nil Policy = no drafts" must be honored. No
// DraftUsedEvent should be emitted even when a
// draft provider is wired.
func TestLoop_DraftDisabledWhenConfigNil(t *testing.T) {
	verifier := echoProvider("verifier")
	draftProv := &draftStubProvider{name: "draft", text: "1. do A"}
	l := makeLoopWithDraft(t, verifier, nil, draftProv, nil, nil)
	ch, err := l.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	for ev := range ch {
		if _, ok := ev.(DraftUsedEvent); ok {
			t.Error("DraftUsedEvent should NOT be emitted when Draft policy is nil")
		}
	}
}

// TestLoop_DraftModeOff — the loop must never
// emit a draft event when the policy's mode is Off
// even if provider is wired.
func TestLoop_DraftModeOff(t *testing.T) {
	verifier := echoProvider("verifier")
	draftProv := &draftStubProvider{name: "draft", text: "1. do A"}
	p, _ := draft.NewPolicy(draft.ModeOff, "draft", "verifier", nil)
	l := makeLoopWithDraft(t, verifier, p, draftProv, nil, nil)
	ch, err := l.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	for ev := range ch {
		if _, ok := ev.(DraftUsedEvent); ok {
			t.Error("DraftUsedEvent should NOT be emitted when ModeOff")
		}
	}
}

// TestLoop_DraftBalanced_OnePerRun — ModeBalanced
// must draft on step 0 only, not on follow-up
// steps. The verifier emits a stop on every call,
// so the loop should run exactly one verifier call
// (and one draft call).
func TestLoop_DraftBalanced_OnePerRun(t *testing.T) {
	verifier := echoProvider("verifier")
	draftProv := &draftStubProvider{name: "draft", text: "1. do A 2. do B 3. do C"}
	p, _ := draft.NewPolicy(draft.ModeBalanced, "draft", "verifier", nil)
	l := makeLoopWithDraft(t, verifier, p, draftProv, nil, nil)
	ch, err := l.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	var drafts []DraftUsedEvent
	for ev := range ch {
		if d, ok := ev.(DraftUsedEvent); ok {
			drafts = append(drafts, d)
		}
	}
	if len(drafts) != 1 {
		t.Fatalf("got %d DraftUsedEvents, want 1 (ModeBalanced = one draft per Run)", len(drafts))
	}
	if drafts[0].DraftModel != "draft" {
		t.Errorf("DraftModel = %q, want draft", drafts[0].DraftModel)
	}
	if drafts[0].VerifierModel != "verifier" {
		t.Errorf("VerifierModel = %q, want verifier", drafts[0].VerifierModel)
	}
}

// TestLoop_DraftAlways_OnePerStep — ModeAlways
// drafts on every step. The verifier stops after
// step 0 so we expect exactly one draft.
func TestLoop_DraftAlways_OnePerStep(t *testing.T) {
	verifier := echoProvider("verifier")
	draftProv := &draftStubProvider{name: "draft", text: "1. do A 2. do B 3. do C 4. do D"}
	p, _ := draft.NewPolicy(draft.ModeAlways, "draft", "verifier", nil)
	l := makeLoopWithDraft(t, verifier, p, draftProv, nil, nil)
	ch, err := l.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for ev := range ch {
		if _, ok := ev.(DraftUsedEvent); ok {
			count++
		}
	}
	if count != 1 {
		t.Errorf("got %d DraftUsedEvents, want 1 (one Run, one step)", count)
	}
}

// TestLoop_DraftDecisionUsed — when the draft's
// plan has high token overlap with the verifier's
// response, the loop should report decision="used"
// and feed savings > 0 to stats.
func TestLoop_DraftDecisionUsed(t *testing.T) {
	verifierText := "step one step two step three step four step five"
	draftText := "step one step two step three step four step five"
	verifier := &stubProvider{
		name: "verifier",
		scripts: [][]llm.Delta{{
			{Role: llm.RoleAssistant, Content: verifierText},
			{FinishReason: "stop", Usage: &llm.Usage{Input: 5, Output: 20, Total: 25}},
		}},
	}
	draftProv := &draftStubProvider{name: "draft", text: draftText}
	p, _ := draft.NewPolicy(draft.ModeAlways, "draft", "verifier", nil)
	rec := stats.NewMemory()
	l := makeLoopWithDraft(t, verifier, p, draftProv, nil, rec)
	ch, err := l.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	var got DraftUsedEvent
	found := false
	for ev := range ch {
		if d, ok := ev.(DraftUsedEvent); ok {
			got = d
			found = true
		}
	}
	if !found {
		t.Fatal("expected a DraftUsedEvent")
	}
	if got.Decision != "used" {
		t.Errorf("Decision = %q, want used (identical text)", got.Decision)
	}
	if got.Savings <= 0 {
		t.Errorf("Savings = %d, want > 0", got.Savings)
	}
	if rec.TotalSaved() <= 0 {
		t.Errorf("stats TotalSaved = %d, want > 0", rec.TotalSaved())
	}
}

// TestLoop_DraftDecisionOverridden — when the
// draft and verifier texts are disjoint, the loop
// should report decision="overridden", feed
// savings=0 to stats, and write a record to the
// override sink.
func TestLoop_DraftDecisionOverridden(t *testing.T) {
	verifier := &stubProvider{
		name: "verifier",
		scripts: [][]llm.Delta{{
			{Role: llm.RoleAssistant, Content: "unrelated response here"},
			{FinishReason: "stop", Usage: &llm.Usage{Input: 5, Output: 50, Total: 55}},
		}},
	}
	draftProv := &draftStubProvider{name: "draft", text: "completely different plan one two three four five"}
	p, _ := draft.NewPolicy(draft.ModeAlways, "draft", "verifier", nil)
	sink := &draftRecordingSink{}
	rec := stats.NewMemory()
	l := makeLoopWithDraft(t, verifier, p, draftProv, sink, rec)
	ch, err := l.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	var got DraftUsedEvent
	found := false
	for ev := range ch {
		if d, ok := ev.(DraftUsedEvent); ok {
			got = d
			found = true
		}
	}
	if !found {
		t.Fatal("expected a DraftUsedEvent")
	}
	if got.Decision != "overridden" {
		t.Errorf("Decision = %q, want overridden", got.Decision)
	}
	if got.Savings != 0 {
		t.Errorf("Savings = %d, want 0 for override", got.Savings)
	}
	records := sink.snapshot()
	if len(records) != 1 {
		t.Errorf("override sink got %d records, want 1", len(records))
	}
	if len(records) == 1 {
		if records[0].DraftModel != "draft" {
			t.Errorf("sink DraftModel = %q", records[0].DraftModel)
		}
		if records[0].VerifierModel != "verifier" {
			t.Errorf("sink VerifierModel = %q", records[0].VerifierModel)
		}
		if !strings.Contains(records[0].DraftText, "completely different plan") {
			t.Errorf("sink DraftText = %q, want contains 'completely different plan'", records[0].DraftText)
		}
		if !strings.Contains(records[0].VerifierText, "unrelated response") {
			t.Errorf("sink VerifierText = %q", records[0].VerifierText)
		}
	}
	if rec.TotalSaved() != 0 {
		t.Errorf("stats TotalSaved = %d, want 0 for override", rec.TotalSaved())
	}
}

// TestLoop_DraftSystemMessageInjected — after the
// draft call, the conversation must contain a
// "[draft plan] ..." system message so the verifier
// sees it on its first turn.
func TestLoop_DraftSystemMessageInjected(t *testing.T) {
	verifier := &stubProvider{
		name: "verifier",
		scripts: [][]llm.Delta{{
			{Role: llm.RoleAssistant, Content: "ok"},
			{FinishReason: "stop", Usage: &llm.Usage{Input: 5, Output: 1, Total: 6}},
		}},
	}
	draftProv := &draftStubProvider{name: "draft", text: "do A first"}
	p, _ := draft.NewPolicy(draft.ModeAlways, "draft", "verifier", nil)
	l := makeLoopWithDraft(t, verifier, p, draftProv, nil, nil)
	ch, err := l.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	found := false
	for _, m := range l.Messages {
		if m.Role == llm.RoleSystem && strings.Contains(m.Content, "[draft plan]") {
			found = true
			if !strings.Contains(m.Content, "do A first") {
				t.Errorf("draft system message missing plan text: %q", m.Content)
			}
		}
	}
	if !found {
		t.Error("no [draft plan] system message was injected into Messages")
	}
}

// TestLoop_DraftProviderFailureIsSilent — a draft
// provider error must not break the verifier run.
// The loop should still emit DoneEvent and no
// DraftUsedEvent.
func TestLoop_DraftProviderFailureIsSilent(t *testing.T) {
	verifier := echoProvider("verifier")
	failing := &failingDraftProvider{}
	p, _ := draft.NewPolicy(draft.ModeAlways, "draft", "verifier", nil)
	l := makeLoopWithDraft(t, verifier, p, failing, nil, nil)
	ch, err := l.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	var sawDone, sawDraft bool
	var sawErr bool
	for ev := range ch {
		switch ev.(type) {
		case DoneEvent:
			sawDone = true
		case DraftUsedEvent:
			sawDraft = true
		case ErrorEvent:
			sawErr = true
		}
	}
	if !sawDone {
		t.Error("verifier should have completed despite draft failure")
	}
	if sawDraft {
		t.Error("no DraftUsedEvent should fire on draft failure")
	}
	if sawErr {
		t.Error("draft failure must not surface as ErrorEvent")
	}
}

// makeLoopWithDraft builds a Loop with optional F11
// wiring.
func makeLoopWithDraft(t *testing.T, p llm.Provider, dp *draft.Policy, draftProv llm.Provider, sink DraftOverrideSink, rec stats.Recorder) *Loop {
	t.Helper()
	cfg := LoopConfig{
		Provider: p,
		Registry: tools.NewRegistry(),
		System:   "",
		MaxSteps: 5,
	}
	if dp != nil {
		cfg.Draft = dp
	}
	if draftProv != nil {
		cfg.DraftProvider = draftProv
	}
	if sink != nil {
		cfg.DraftOverrideSink = sink
	}
	if rec != nil {
		cfg.Stats = rec
	}
	l, err := NewLoop(cfg)
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	return l
}

// failingDraftProvider always returns an error from
// Complete. Used to verify the loop survives a
// broken draft provider.
type failingDraftProvider struct{}

func (failingDraftProvider) Name() string { return "failing" }
func (failingDraftProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	return nil, context.DeadlineExceeded // any error works
}
