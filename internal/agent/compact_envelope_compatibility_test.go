package agent

import (
	"testing"

	"supercli/internal/llm"
)

func TestCompactEnvelopeKeepsSummaryBodyAndLegacyRecognition(t *testing.T) {
	body := "Goal: fix quantity 0.\nDone: pricing.cjs fixed; tests passed.\nState: catalog unchanged.\nPending: docs/API.md."
	current := WrapCompactSummary(" \n" + body + "\n ")
	legacy := legacyCompactSummaryPreamble + body + legacyCompactSummaryEpilogue
	if current != compactSummaryPreamble+body+compactSummaryEpilogue {
		t.Fatal("summary facts were changed")
	}
	if len(current) >= len(legacy) {
		t.Fatal("resume framing was not shortened")
	}
	for _, envelope := range []string{current, legacy} {
		for _, tc := range []struct {
			name string
			msg  llm.Message
			want bool
		}{
			{"summary", llm.Message{Role: llm.RoleUser, Content: envelope}, true},
			{"quoted", llm.Message{Role: llm.RoleUser, Content: "Please explain:\n" + envelope}, false},
			{"extra question", llm.Message{Role: llm.RoleUser, Content: envelope + "\nWhy?"}, false},
			{"named human", llm.Message{Role: llm.RoleUser, Name: "human", Content: envelope}, false},
			{"assistant", llm.Message{Role: llm.RoleAssistant, Content: envelope}, false},
			{"attachment", llm.Message{Role: llm.RoleUser, Content: envelope, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: envelope}}}, false},
		} {
			if got := IsLegacyCompactionSummary(tc.msg); got != tc.want {
				t.Fatalf("%s: summary recognition=%v, want %v", tc.name, got, tc.want)
			}
		}
	}
	for _, content := range []string{
		compactSummaryPreamble + compactSummaryEpilogue,
		legacyCompactSummaryPreamble + legacyCompactSummaryEpilogue,
		compactSummaryPreamble + body + legacyCompactSummaryEpilogue,
		legacyCompactSummaryPreamble + body + compactSummaryEpilogue,
	} {
		if IsLegacyCompactionSummary(llm.Message{Role: llm.RoleUser, Content: content}) {
			t.Fatal("empty or mixed envelope recognized as generated summary")
		}
	}
	t.Logf("framing bytes: %d -> %d; saved %d", len(legacy)-len(body), len(current)-len(body), len(legacy)-len(current))
}

func TestCompactEnvelopeDoesNotCauseRepeatedSummaryRequests(t *testing.T) {
	for _, summary := range []string{
		WrapCompactSummary("Goal: finish existing work."),
		legacyCompactSummaryPreamble + "Goal: finish existing work." + legacyCompactSummaryEpilogue,
	} {
		messages := []llm.Message{
			{Role: llm.RoleSystem, Content: "unchanged system"},
			{Role: llm.RoleUser, Content: summary},
			{Role: llm.RoleAssistant, Content: "observed source"},
			{Role: llm.RoleUser, Content: "current work"},
		}
		if hasFreshCompactablePrefix(messages, 2) {
			t.Fatal("summary-only prefix would trigger another summarizer call")
		}
		if !hasFreshCompactablePrefix(messages, 3) {
			t.Fatal("new observed work was lost")
		}
	}
	question := []llm.Message{{Role: llm.RoleUser, Content: compactSummaryPreamble + "Why does this appear?"}}
	if !hasFreshCompactablePrefix(question, len(question)) {
		t.Fatal("human question incorrectly treated as generated summary")
	}
}

func TestCompactEnvelopeCannotRestoreDownloadPermission(t *testing.T) {
	for _, summary := range []string{
		WrapCompactSummary("Download the files into Downloads."),
		legacyCompactSummaryPreamble + "Download the files into Downloads." + legacyCompactSummaryEpilogue,
	} {
		loop := &Loop{}
		loop.restoreUserDownloadHistory([]llm.Message{{Role: llm.RoleUser, Content: summary}})
		if len(loop.downloadHumanContext) != 0 {
			t.Fatal("summary became a human download authorization")
		}
	}
}
