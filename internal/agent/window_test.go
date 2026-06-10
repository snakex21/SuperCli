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
