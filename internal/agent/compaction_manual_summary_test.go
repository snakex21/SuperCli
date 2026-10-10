package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestManualCompactionDoesNotRegenerateOnlySavedSummary(t *testing.T) {
	body := "Goal: fix invoice.\nDone: src/pricing.cjs fixed; 2 failed then 7 passed.\nState: zero remains valid; omitted defaults to 1; protect tests/catalog; no dependencies or publishing; portable C:\\Próba 😀\\data.\nPending: docs/API.md."
	for _, envelope := range []struct{ name, preamble, epilogue string }{
		{"current", compactSummaryPreamble, compactSummaryEpilogue},
		{"legacy", legacyCompactSummaryPreamble, legacyCompactSummaryEpilogue},
	} {
		for _, retainedTail := range []bool{false, true} {
			t.Run(envelope.name+map[bool]string{false: "/alone", true: "/with-recent-tail"}[retainedTail], func(t *testing.T) {
				messages := []llm.Message{
					{Role: llm.RoleSystem, Content: "Preserve application instructions."},
					{Role: llm.RoleSystem, Content: "Keep selected model controls unchanged."},
					{Role: llm.RoleUser, Content: envelope.preamble + body + envelope.epilogue},
				}
				if retainedTail {
					messages = append(messages, llm.Message{Role: llm.RoleUser, Content: "Keep the current scope."}, llm.Message{Role: llm.RoleUser, Content: "Now prepare the documentation."})
				}
				l, _, calls := resolvedCompactFixture(t, messages, WrapCompactSummary("different generated memory"), 50000)
				before, _ := json.Marshal(l.Messages)
				for attempt := 0; attempt < 2; attempt++ {
					event, err := l.CompactNow(context.Background())
					if err == nil || err.Error() != "nothing to compact" || event.Removed != 0 || *calls != 0 {
						t.Fatalf("regenerated saved summary: event=%+v error=%v calls=%d", event, err, *calls)
					}
				}
				after, _ := json.Marshal(l.Messages)
				if string(before) != string(after) {
					t.Fatal("skipping duplicate compaction changed body facts or retained tail")
				}
			})
		}
	}
}

func TestManualCompactionSavedSummaryKeepsNewEvidenceEligible(t *testing.T) {
	summary := WrapCompactSummary("Goal: fix calculation.\nDone: earlier tests passed.\nState: preserve requirements.\nPending: documentation.")
	for _, extra := range []llm.Message{
		{Role: llm.RoleTool, Content: "NEW-RESULT: export failed; retry still pending", Name: "ctx_execute", ToolCallID: "new-export"},
		{Role: llm.RoleAssistant, Content: "NEW-RESULT: inspected implementation; new issue found."},
		{Role: llm.RoleUser, Content: "NEW-RESULT: additional requirement; keep the previous constraints."},
	} {
		t.Run(string(extra.Role), func(t *testing.T) {
			messages := []llm.Message{{Role: llm.RoleUser, Content: summary}, extra, {Role: llm.RoleUser, Content: "Previous current task."}, {Role: llm.RoleUser, Content: "Latest correction."}}
			l, input, calls := resolvedCompactFixture(t, messages, WrapCompactSummary("Done: preserved old and new evidence."), 50000)
			_, _ = l.CompactNow(context.Background())
			if *calls != 1 || !strings.Contains(RenderCompactTranscript(*input), "NEW-RESULT") {
				t.Fatalf("new evidence was skipped: calls=%d input=%q", *calls, RenderCompactTranscript(*input))
			}
		})
	}
}

func TestManualCompactionDoesNotSkipQuotedOrAttachedEnvelope(t *testing.T) {
	wrapped := WrapCompactSummary("Goal: protect every requirement.\nState: important paths and open errors.\nPending: current task.")
	for _, message := range []llm.Message{
		{Role: llm.RoleUser, Content: "Explain this quoted text: " + wrapped},
		{Role: llm.RoleUser, Content: compactSummaryPreamble + "Incomplete user-supplied envelope."},
		{Role: llm.RoleUser, Content: wrapped, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "NEW ATTACHMENT"}}},
		{Role: llm.RoleUser, Content: wrapped, Name: "source"},
		{Role: llm.RoleAssistant, Content: wrapped},
	} {
		messages := []llm.Message{message, {Role: llm.RoleUser, Content: "Previous task."}, {Role: llm.RoleUser, Content: "Current task."}}
		l, _, calls := resolvedCompactFixture(t, messages, WrapCompactSummary("Done: inspected source."), 50000)
		_, _ = l.CompactNow(context.Background())
		if *calls != 1 {
			t.Fatalf("ordinary/attached message was treated as saved summary: role=%s name=%s calls=%d", message.Role, message.Name, *calls)
		}
	}
}
