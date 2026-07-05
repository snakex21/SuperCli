package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestIsContextLimitErr(t *testing.T) {
	cases := map[string]bool{
		"This model's maximum context length is 8192 tokens.": true,
		"error code context_length_exceeded":                  true,
		"request failed: too many tokens":                     true,
		"connection refused":                                  false,
	}
	for msg, want := range cases {
		if got := isContextLimitErr(errors.New(msg)); got != want {
			t.Errorf("isContextLimitErr(%q) = %v, want %v", msg, got, want)
		}
	}
	if isContextLimitErr(nil) {
		t.Error("nil error must not match")
	}
}

func TestExtractContextLimit(t *testing.T) {
	cases := map[string]int{
		"This model's maximum context length is 8192 tokens": 8192,
		"model loaded with context length of only 4096":      4096,
		"too many tokens":                                    0,
	}
	for msg, want := range cases {
		if got := extractContextLimit(msg); got != want {
			t.Errorf("extractContextLimit(%q) = %d, want %d", msg, got, want)
		}
	}
}

func TestMaybeAutoCompact(t *testing.T) {
	echo, _ := llm.NewEcho("test")
	l := &Loop{
		provider:  echo,
		modelID:   "test",
		windowFor: func(string) int { return 100 }, // tiny window
		summarizer: func(ctx context.Context, p llm.Provider, msgs []llm.Message) (string, error) {
			return "SUMMARY", nil
		},
	}
	l.Messages = []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: strings.Repeat("x", 2000)}, // ~500 tokens > 80
		{Role: llm.RoleAssistant, Content: "ok"},
	}
	out := make(chan Event, 4)
	l.maybeAutoCompact(context.Background(), out, "")
	if len(l.Messages) != 2 { // sys + summary
		t.Fatalf("expected compaction to [sys, summary], got %d messages", len(l.Messages))
	}
	if l.Messages[1].Content != "SUMMARY" {
		t.Errorf("summary message = %q", l.Messages[1].Content)
	}
	select {
	case ev := <-out:
		ac, ok := ev.(AutoCompactEvent)
		if !ok || ac.Removed != 2 || ac.Reason != "auto" {
			t.Errorf("unexpected event %+v", ev)
		}
	default:
		t.Error("expected AutoCompactEvent")
	}

	// Under threshold: no-op.
	before := len(l.Messages)
	l.maybeAutoCompact(context.Background(), out, "")
	if len(l.Messages) != before {
		t.Error("compacted below threshold")
	}
}

// TestMaybeAutoCompact_KeepsLastTurn: when the bulk sits in OLDER
// turns, the summary replaces only those; the last user turn (the
// work in progress) survives verbatim after the summary — and the
// summarizer never even sees it.
func TestMaybeAutoCompact_KeepsLastTurn(t *testing.T) {
	echo, _ := llm.NewEcho("test")
	var summarized []llm.Message
	l := &Loop{
		provider:  echo,
		modelID:   "test",
		windowFor: func(string) int { return 2000 },
		summarizer: func(ctx context.Context, p llm.Provider, msgs []llm.Message) (string, error) {
			summarized = append([]llm.Message(nil), msgs...)
			return "SUMMARY", nil
		},
	}
	l.Messages = []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: strings.Repeat("x", 6000)}, // old bulk
		{Role: llm.RoleAssistant, Content: "done earlier"},
		{Role: llm.RoleUser, Content: "current question"},
		{Role: llm.RoleAssistant, Content: "working on it"},
	}
	l.maybeAutoCompact(context.Background(), nil, "")

	want := []string{"sys", "SUMMARY", "current question", "working on it"}
	if len(l.Messages) != len(want) {
		t.Fatalf("Messages = %d entries, want %d: %+v", len(l.Messages), len(want), l.Messages)
	}
	for i, w := range want {
		if l.Messages[i].Content != w {
			t.Errorf("Messages[%d] = %q, want %q", i, l.Messages[i].Content, w)
		}
	}
	for _, m := range summarized {
		if m.Content == "current question" || m.Content == "working on it" {
			t.Errorf("last turn leaked into the summarizer input: %q", m.Content)
		}
	}
}

// TestMaybeAutoCompact_HugeLastTurnFallsBackToFull: when the last
// turn alone still eats more than half the window, cutting at the
// turn boundary would leave the context over budget — everything is
// summarized (the historical behaviour, exercised above in
// TestMaybeAutoCompact with a single huge user message).
func TestMaybeAutoCompact_HugeLastTurnFallsBackToFull(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "small old turn"},
		{Role: llm.RoleAssistant, Content: "ok"},
		{Role: llm.RoleUser, Content: strings.Repeat("y", 6000)}, // huge current turn
	}
	if got := compactSplit(msgs, 2000); got != len(msgs) {
		t.Errorf("compactSplit = %d, want %d (full compaction)", got, len(msgs))
	}
}
