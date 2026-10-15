package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
)

// A learned prefill budget below the hard window must not abort the run when
// the summary would not shrink the older prefix (observed 717 -> 730 tokens).
// The exact history stays and the same prefix is not summarized again.
func TestBudgetCompactionKeepsHistoryWhenSummaryDoesNotShrink(t *testing.T) {
	profiles := llm.LoadPrefillProfiles(t.TempDir())
	profiles.Observe("conn", "m", llm.PrefillSample{InputTokens: 40_000, TTFT: 30 * time.Second})
	echo, _ := llm.NewEcho("m")
	calls := 0
	l := &Loop{
		provider: echo, modelID: "m", contextProvider: "conn", prefillProfiles: profiles,
		windowFor: func(string) int { return 100_000 },
		summarizer: func(context.Context, llm.Provider, []llm.Message) (string, error) {
			calls++
			return strings.Repeat("y", 90_000), nil
		},
	}
	l.Messages = []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: strings.Repeat("x", 66_000)},
		{Role: llm.RoleAssistant, Content: "old answer"},
		{Role: llm.RoleUser, Content: "previous correction"},
		{Role: llm.RoleAssistant, Content: "previous answer"},
		{Role: llm.RoleUser, Content: "current task"},
	}
	before := append([]llm.Message(nil), l.Messages...)
	for i := 0; i < 2; i++ {
		if err := l.maybeAutoCompact(context.Background(), nil, ""); err != nil {
			t.Fatalf("step %d: budget compaction aborted the run: %v", i, err)
		}
	}
	if !reflect.DeepEqual(l.Messages, before) {
		t.Fatal("ineffective summary changed history")
	}
	if calls != 1 {
		t.Fatalf("summary calls=%d, want 1 (rejected prefix must not be paid again)", calls)
	}
}
