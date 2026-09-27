package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestPruneDoesNotRewriteOmittedCompletedTools(t *testing.T) {
	for _, kind := range []string{"older completed turn", "latest completed turn"} {
		t.Run(kind, func(t *testing.T) {
			messages := []llm.Message{{Role: llm.RoleUser, Content: "old task"}}
			for i := 0; i < 4; i++ {
				id := fmt.Sprintf("old-%d", i)
				read := completedRead(id, "old.go", strings.Repeat("archived old result ", 1200))
				messages = append(messages, read[:2]...)
			}
			messages = append(messages, llm.Message{Role: llm.RoleAssistant, Content: "done"})
			if kind == "older completed turn" {
				messages = append(messages,
					llm.Message{Role: llm.RoleUser, Content: "another completed request"},
					llm.Message{Role: llm.RoleAssistant, Content: "another final reply"})
			}
			messages = append(messages,
				llm.Message{Role: llm.RoleUser, Content: "current request"},
				llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "active", Name: "read_lines", Arguments: "{}"}}},
				llm.Message{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "active", Content: "live evidence"})
			l, _, _ := resolvedCompactFixture(t, messages, "", 5000)
			l.Messages[0].Content = strings.Repeat("fixed policy ", 1000)
			l.pruneProtect = 1
			before, _ := json.Marshal(l.Messages)
			estimate := l.estimateNextRequestTokensRaw()
			writer := l.writer.(*recordingWriter)
			originalWrites := len(writer.messages)
			probe := &pruneArchiveProbe{}
			l.toolOutputs = probe
			events := make(chan Event, 1)
			reclaimed := l.maybePruneToolResults(context.Background(), events)
			after, _ := json.Marshal(l.Messages)
			if reclaimed != 0 || string(before) != string(after) || len(events) != 0 {
				t.Errorf("rewrote omitted results: reported saving=%d; actual request %d -> %d", reclaimed, estimate, l.estimateNextRequestTokensRaw())
			}
			if len(writer.messages) != originalWrites || probe.saves != 0 {
				t.Error("refused prune wrote session messages")
			}
			t.Logf("request estimate %d -> %d; reported saving=%d", estimate, l.estimateNextRequestTokensRaw(), reclaimed)
		})
	}
}

func TestPruneGainGateDoesNotCountDiscardedReasoning(t *testing.T) {
	l := pruneLoop(t, 10000, 1)
	bigToolTurn(l, "earlier task", 4, 6000)
	l.Messages[len(l.Messages)-1] = policyReply("discarded reasoning")
	l.Messages[len(l.Messages)-1].Parts[0].Reasoning.Tokens = 100000
	l.Messages = append(l.Messages,
		llm.Message{Role: llm.RoleUser, Content: "current task"},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "active", Name: "read_lines", Arguments: "{}"}}},
		llm.Message{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "active", Content: "current evidence"})
	l.discardPreviousReasoning = true
	before := l.estimateNextRequestTokensRaw()
	if reclaimed := l.maybePruneToolResults(context.Background(), nil); reclaimed <= 0 {
		t.Fatal("discarded reasoning inflated the gain threshold and blocked useful pruning")
	} else if after := l.estimateNextRequestTokensRaw(); before-after != reclaimed {
		t.Fatalf("reported saving=%d, request estimate %d -> %d", reclaimed, before, after)
	}
}

func TestPruneGainAccountsForResultsReenteringRecentBudget(t *testing.T) {
	messages := []llm.Message{{Role: llm.RoleUser, Content: "inspect files"}}
	old := completedRead("older", "older.go", strings.Repeat("o", 2500))
	newer := completedRead("newer", "newer.go", strings.Repeat("n", 3000))
	messages = append(messages, old[:2]...)
	messages = append(messages, newer...)
	messages = append(messages,
		llm.Message{Role: llm.RoleUser, Content: "current task"},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "live", Name: "read_lines", Arguments: "{}"}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "live", Name: "read_lines", Content: "current evidence"})
	l, _, _ := resolvedCompactFixture(t, messages, "", 1500)
	l.Messages[0].Content = strings.Repeat("fixed policy ", 200)
	l.pruneProtect = 1
	before, _ := json.Marshal(l.Messages)
	oldEstimate := l.estimateNextRequestTokensRaw()
	if gain := l.maybePruneToolResults(context.Background(), nil); gain != 0 {
		t.Errorf("accepted marginal replacement: claimed gain=%d, actual request %d -> %d", gain, oldEstimate, l.estimateNextRequestTokensRaw())
	}
	after, _ := json.Marshal(l.Messages)
	if string(before) != string(after) {
		t.Error("rewrote the prefix for savings below the cache-rewrite threshold")
	}
}

func TestPruneProjectionMapsHiddenAndOmittedTools(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "hidden task"},
		{Role: llm.RoleAssistant, Content: "hidden answer"},
		{Role: llm.RoleUser, Content: "completed request"},
	}
	messages = append(messages, completedRead("omitted", "old.go", strings.Repeat("omitted old evidence ", 600))...)
	messages = append(messages, llm.Message{Role: llm.RoleUser, Content: "active investigation"})
	for i := 0; i < 4; i++ {
		read := completedRead(fmt.Sprintf("work-%d", i), "work.go", strings.Repeat(fmt.Sprintf("EVIDENCE_%d\n", i), 400))
		messages = append(messages, read[:2]...)
	}
	messages = append(messages,
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "current", Name: "read_lines", Arguments: "{}"}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "current", Name: "read_lines", Content: "LATEST-EVIDENCE"})
	l, _, _ := resolvedCompactFixture(t, messages, "", 5000)
	l.pruneProtect = 1
	probe := &pruneArchiveProbe{}
	l.toolOutputs = probe
	if err := l.HideRange(1, 3); err != nil {
		t.Fatal(err)
	}
	before := append([]llm.Message(nil), l.Messages...)
	estimate := l.estimateNextRequestTokensRaw()
	gain := l.maybePruneToolResults(context.Background(), nil)
	if gain <= 0 || gain != estimate-l.estimateNextRequestTokensRaw() {
		t.Fatalf("wrong projected savings: %d, request %d -> %d", gain, estimate, l.estimateNextRequestTokensRaw())
	}
	if probe.saves != 1 {
		t.Fatalf("archive writes=%d want=1", probe.saves)
	}
	pruned := 0
	for i, message := range l.Messages {
		if before[i].Role == llm.RoleTool && strings.HasPrefix(before[i].ToolCallID, "work-") {
			pruned++
			if !strings.HasPrefix(message.Content, pruneMarkerPrefix) {
				t.Fatalf("active older result %s not pruned", message.ToolCallID)
			}
			_, args, ok := strings.Cut(message.Content, "read_output ")
			if !ok {
				t.Fatal("pruned evidence has no retrieval reference")
			}
			result := l.invoke(context.Background(), llm.ToolCall{ID: "retrieve", Name: "read_output", Arguments: strings.TrimSuffix(args, "]")}, make(chan Event, 8))
			if result.failed || len(result.followUps) != 1 || !strings.Contains(result.followUps[0].Content, before[i].Content) {
				t.Fatal("mapped reference could not retrieve the original evidence")
			}
		} else {
			got, _ := json.Marshal(message)
			want, _ := json.Marshal(before[i])
			if string(got) != string(want) {
				t.Fatalf("protected/omitted message %d changed", i)
			}
		}
	}
	if pruned != 4 || l.HiddenCount() != 2 {
		t.Fatal("wrong victim/visibility mapping")
	}
	assertEvidencePairs(t, l.providerMessages())
}
