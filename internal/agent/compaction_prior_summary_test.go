package agent

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestCompactTranscriptPriorSummaryRetainsEveryBodyByte(t *testing.T) {
	body := "Goal: fix billing.\r\nDone: src/pricing.cjs fixed; observed 2 failed then 7 passed.\n" +
		"State: quantity 0 is valid; default 1. Protect tests/catalog. No dependencies. Do not publish.\n" +
		"Keep ALL data in C:\\Program Files\\Próba 😀\\data.\n" +
		"Pending: failed worker w-18 remains; docs/API.md has not been updated.\n" +
		"files_modified: src/pricing.cjs\nloaded_tools: patch_file, ctx_execute\n"
	for _, envelope := range []struct{ name, preamble, epilogue string }{
		{"current", compactSummaryPreamble, compactSummaryEpilogue},
		{"legacy", legacyCompactSummaryPreamble, legacyCompactSummaryEpilogue},
	} {
		t.Run(envelope.name, func(t *testing.T) {
			messages := []llm.Message{
				{Role: llm.RoleUser, Content: envelope.preamble + body + envelope.epilogue},
				{Role: llm.RoleUser, Content: "Later correction: keep the earlier prohibitions, but documentation can wait."},
			}
			before, _ := json.Marshal(messages)
			got := RenderCompactTranscript(messages)
			want := "[user] " + body + "\n[user] " + messages[1].Content + "\n"
			if got != want {
				t.Fatalf("prior summary body or subsequent correction changed: got %q want %q", got, want)
			}
			if saved := got[len("[user] ") : len("[user] ")+len(body)]; sha256.Sum256([]byte(saved)) != sha256.Sum256([]byte(body)) {
				t.Fatal("summary facts are no longer byte-identical")
			}
			after, _ := json.Marshal(messages)
			if string(after) != string(before) || WrapCompactSummary(body) != compactSummaryPreamble+strings.TrimSpace(body)+compactSummaryEpilogue {
				t.Fatal("canonical history or next main request framing changed")
			}
		})
	}
}

func TestCompactTranscriptOnlyRemovesCompletePlainSummaryEnvelope(t *testing.T) {
	wrapped := WrapCompactSummary("State: keep all constraints, including this quoted text:\n" + compactSummaryEpilogue)
	for _, tc := range []struct {
		name string
		msg  llm.Message
	}{
		{"ordinary user", llm.Message{Role: llm.RoleUser, Content: "Keep the instructions: " + wrapped}},
		{"quoted user", llm.Message{Role: llm.RoleUser, Content: "\"" + wrapped + "\""}},
		{"incomplete prefix", llm.Message{Role: llm.RoleUser, Content: compactSummaryPreamble + "State: user requirement"}},
		{"empty body", llm.Message{Role: llm.RoleUser, Content: compactSummaryPreamble + " \n " + compactSummaryEpilogue}},
		{"assistant", llm.Message{Role: llm.RoleAssistant, Content: wrapped}},
		{"tool", llm.Message{Role: llm.RoleTool, Content: wrapped}},
		{"named user", llm.Message{Role: llm.RoleUser, Name: "source", Content: wrapped}},
		{"multipart", llm.Message{Role: llm.RoleUser, Content: wrapped, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "do not omit my attachment"}}}},
		{"tool-call user", llm.Message{Role: llm.RoleUser, Content: wrapped, ToolCalls: []llm.ToolCall{{ID: "x", Name: "task", Arguments: `{}`}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := []llm.Message{tc.msg}
			if got, want := RenderCompactTranscript(messages), originalCompactTranscript(messages); got != want {
				t.Fatalf("non-summary transcript changed: got %q want %q", got, want)
			}
		})
	}
}

// Optional offline measurement on an actual completed compaction receipt.
// No provider call or new fixture output is fabricated by this test.
func TestCompactPriorSummaryReceiptSavings(t *testing.T) {
	path := os.Getenv("SUPERCLI_PRIOR_SUMMARY_RECEIPT")
	if path == "" {
		t.Skip("opt-in completed receipt measurement, no inference")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Summary string `json:"summary"`
		Passed  bool   `json:"passed"`
	}
	if err := json.Unmarshal(data, &receipt); err != nil || !receipt.Passed {
		t.Fatalf("valid completed compaction receipt required: %v", err)
	}
	messages := []llm.Message{{Role: llm.RoleUser, Content: receipt.Summary}}
	if !IsLegacyCompactionSummary(messages[0]) {
		t.Fatal("receipt does not contain a complete prior summary envelope")
	}
	before, after := originalCompactTranscript(messages), RenderCompactTranscript(messages)
	if len(after) >= len(before) {
		t.Fatal("prior summary framing was not removed")
	}
	// Everything removed is the exact fixed preamble and epilogue; body facts
	// are neither parsed nor normalized and retain their original SHA-256.
	body := compactTranscriptContent(messages[0])
	if after != "[user] "+body+"\n" || !strings.Contains(before, body) {
		t.Fatal("receipt facts were altered")
	}
	t.Logf("actual prior summary: before=%d B/%d estimated tokens after=%d B/%d estimated tokens; exact body SHA256=%x",
		len(before), llm.EstimateTokens([]llm.Message{{Role: llm.RoleUser, Content: before}}),
		len(after), llm.EstimateTokens([]llm.Message{{Role: llm.RoleUser, Content: after}}), sha256.Sum256([]byte(body)))
}
