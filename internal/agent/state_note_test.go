package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"supercli/internal/llm"
)

// noteProvider answers summary requests with a state note that forgets the
// user's constraints (as a model might) and records each request's input.
type noteProvider struct {
	mu     sync.Mutex
	inputs []string
	round  int
	note   func(round int) string
}

func (*noteProvider) Name() string { return "note-model" }

func (p *noteProvider) Complete(_ context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.mu.Lock()
	p.round++
	round := p.round
	if len(msgs) > 1 {
		p.inputs = append(p.inputs, msgs[1].Content)
	}
	p.mu.Unlock()
	text := fmt.Sprintf("Goal: finish the survival night (round %d).\nDone: mobs.\nIn progress and next: zombie spawn.", round)
	if p.note != nil {
		text = p.note(round)
	}
	ch := make(chan llm.Delta, 1)
	ch <- llm.Delta{Content: text, FinishReason: "stop"}
	close(ch)
	return ch, nil
}

// Three compactions in a row: each one updates the previous note (it is in
// the summary input), and the user's constraints survive verbatim even when
// the model's note forgets them.
func TestStateNoteKeepsUserConstraintsAcrossCompactions(t *testing.T) {
	const constraint = "Nigdy nie ruszaj pliku config.toml; tylko snake_case."
	p := &noteProvider{}
	summarize := NewAutoSummarizer(nil)
	ctx := WithCompactWindow(context.Background(), 200_000)
	history := []llm.Message{
		{Role: llm.RoleUser, Content: constraint},
		{Role: llm.RoleAssistant, Content: "OK, config.toml stays untouched."},
		{Role: llm.RoleUser, Content: "Dodaj zombie w nocy."},
		{Role: llm.RoleAssistant, Content: "Zombie added."},
	}
	var summary string
	for round := 1; round <= 3; round++ {
		var err error
		summary, err = summarize(ctx, p, history)
		if err != nil {
			t.Fatalf("compaction %d: %v", round, err)
		}
		if !strings.Contains(summary, constraint) {
			t.Fatalf("compaction %d dropped the user's constraint:\n%s", round, summary)
		}
		if round > 1 && !strings.Contains(p.inputs[round-1], fmt.Sprintf("round %d", round-1)) {
			t.Fatalf("compaction %d did not see the previous note to update it", round)
		}
		history = []llm.Message{
			{Role: llm.RoleUser, Content: summary},
			{Role: llm.RoleUser, Content: fmt.Sprintf("Kolejne zadanie %d.", round)},
			{Role: llm.RoleAssistant, Content: fmt.Sprintf("Zrobione %d.", round)},
		}
	}
	if !strings.Contains(compactionStatePrompt, "UPDATE it") || !strings.Contains(compactionStatePrompt, "Never drop a user decision or constraint") {
		t.Fatal("the instruction no longer asks to update the earlier note")
	}
}

// The note stays within its cap, even for a huge history: an oversized note
// is rejected (history kept) rather than cut, and the cap follows the window.
func TestStateNoteStaysWithinItsCap(t *testing.T) {
	if got := compactNoteMaxChars(40_000); got != 15_000 {
		t.Fatalf("note cap for a 40k window = %d", got)
	}
	if got := compactNoteMaxChars(1_000_000); got != compactSummaryMaxChars {
		t.Fatalf("note cap for a 1M window = %d", got)
	}
	huge := bigHistory(300) // far above the window: chunked input
	ctx := WithCompactWindow(context.Background(), 60_000)
	ok := &noteProvider{note: func(int) string {
		return "Goal: g\nUser decisions and constraints: none\nDone: " + strings.Repeat("x", 8_000)
	}}
	summary, err := NewAutoSummarizer(nil)(ctx, ok, huge)
	if err != nil {
		t.Fatalf("a note within the cap was refused: %v", err)
	}
	if len(summary) > compactNoteMaxChars(60_000)+compactFactsMaxBytes+64_000 {
		t.Fatalf("summary of a huge history is %d bytes", len(summary))
	}
	tooLong := &noteProvider{note: func(int) string { return "Goal: g\nDone: " + strings.Repeat("y", compactNoteMaxChars(60_000)+1) }}
	if _, err := NewAutoSummarizer(nil)(ctx, tooLong, huge[:40]); !errors.Is(err, errCompactionSummaryTooLong) {
		t.Fatalf("an oversized note was accepted: %v", err)
	}
}

// A running worker's tools can read its own transcript for recall.
func TestWorkerRecallTranscriptFromContext(t *testing.T) {
	if _, _, ok := WorkerRecallTranscript(context.Background()); ok {
		t.Fatal("a transcript outside a worker run")
	}
	f, _ := contractFixture(t, false,
		[][]llm.Delta{callStep(tcall("t1", "task", `{"prompt":"spider"}`)), say("Delegated.")},
		[][]llm.Delta{say("spider partial")})
	runTurnText(t, f.parent, "Dodaj pająka.")
	w := f.task.Workers.List()[0]
	id, msgs, ok := WorkerRecallTranscript(context.WithValue(context.Background(), workerSelfKey{}, w))
	if !ok || id != w.ID || len(msgs) == 0 {
		t.Fatalf("worker transcript = %q, %d messages, %v", id, len(msgs), ok)
	}
	found := false
	for _, m := range msgs {
		found = found || strings.Contains(m.TextOnly().Content, "spider")
	}
	if !found {
		t.Fatal("the worker's own task is not in its recall transcript")
	}
}
