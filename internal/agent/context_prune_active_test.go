package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

func TestLatencyPruningKeepsActiveTaskEvidenceAndPrunesCompletedHistory(t *testing.T) {
	l := pruneLoop(t, 128000, 1)
	l.prefillProfiles = llm.LoadPrefillProfiles(t.TempDir())
	l.prefillProfiles.Observe(prefillProfileDefaultScope, "test", llm.PrefillSample{InputTokens: 40000, TTFT: 30 * time.Second})
	bigToolTurn(l, "old completed task", 5, 18000)
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "implement the current task"})
	activeStart := len(l.Messages)
	for i := 0; i < 12; i++ {
		call, result := sourceFixture(fmt.Sprintf("active-%d.go", i), fmt.Sprintf("active-%d", i), 1, 50)
		l.Messages = append(l.Messages, call, result)
	}
	before := append([]llm.Message(nil), l.Messages...)
	est := l.EstimateNextRequestTokens()
	budget, learned := l.PrefillBudget()
	if !learned || est <= budget || est >= int(pruneTriggerFrac*128000) {
		t.Fatalf("fixture is not in latency-only pressure: estimate=%d budget=%d", est, budget)
	}
	if gain := l.maybePruneToolResults(context.Background(), nil); gain <= 0 {
		t.Fatal("completed history stopped being reclaimable")
	}
	prunedOld := false
	for i, message := range l.Messages {
		if i >= activeStart && message.Content != before[i].Content {
			t.Fatal("latency target discarded evidence from an ongoing task")
		}
		prunedOld = prunedOld || (i < activeStart && strings.HasPrefix(message.Content, pruneMarkerPrefix))
	}
	if !prunedOld {
		t.Fatal("no completed old result was pruned")
	}
}

func TestPruningKeepsRetrievedOutputUntilNextUserTurn(t *testing.T) {
	ctx := context.Background()
	reg := tools.NewRegistry()
	reg.EnsureReadOutput()
	body := strings.Repeat("Recovered source observation zażółć, still needed.\n", 100)
	preview := reg.ModelResultContentContext(ctx, "captured", tools.Result{RetainedText: body})
	handle := core.StoredOutputHandle(preview)
	if handle == "" {
		t.Fatal("fixture output was not retained")
	}
	l := pruneLoop(t, 5000, 1)
	l.registry = reg
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "continue using recovered source"})
	call := llm.ToolCall{ID: "recovered", Name: "read_output", Arguments: fmt.Sprintf(`{"handle":%q,"limit":8192}`, handle)}
	result := l.invoke(ctx, call, make(chan Event, 8))
	if result.failed || len(result.followUps) != 1 {
		t.Fatalf("fixture read_output failed: %+v", result)
	}
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}, result.followUps[0])
	for i := 0; i < 4; i++ {
		a, b := sourceFixture(fmt.Sprintf("other-%d.go", i), fmt.Sprintf("other-%d", i), 1, 100)
		l.Messages = append(l.Messages, a, b)
	}
	if gain := l.maybePruneToolResults(ctx, nil); gain <= 0 {
		t.Fatal("hard-window pressure stopped reclaiming ordinary old results")
	}
	if !strings.Contains(l.Messages[2].Content, body) || strings.HasPrefix(l.Messages[2].Content, pruneMarkerPrefix) {
		t.Fatal("retrieved output was archived again during the same task")
	}
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleAssistant, Content: "done"}, llm.Message{Role: llm.RoleUser, Content: "new task"})
	a, b := sourceFixture("new.go", "new", 1, 100)
	l.Messages = append(l.Messages, a, b)
	l.invalidateVisibleEstimate()
	if gain := l.maybePruneToolResults(ctx, nil); gain <= 0 || !strings.HasPrefix(l.Messages[2].Content, pruneMarkerPrefix) {
		t.Fatal("retrieved output remained pinned after the task finished")
	}
}

func TestLatencyPruningCanReclaimCompletedHistoryBeforeFirstNewTool(t *testing.T) {
	l := pruneLoop(t, 128000, 1)
	l.prefillProfiles = llm.LoadPrefillProfiles(t.TempDir())
	l.prefillProfiles.Observe(prefillProfileDefaultScope, "test", llm.PrefillSample{InputTokens: 40000, TTFT: 30 * time.Second})
	bigToolTurn(l, "completed old task", 5, 18000)
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "new task, no tool called yet"})
	if gain := l.maybePruneToolResults(context.Background(), nil); gain <= 0 {
		t.Fatal("new user boundary incorrectly protected all completed history")
	}
}
