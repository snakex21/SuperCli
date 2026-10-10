package agent

import (
	"strings"

	"supercli/internal/llm"
)

// IsLegacyCompactionSummary recognizes the complete envelope appended to old
// transcripts by CompactPrefixWithSummary. Those rows lack origin metadata and
// used a user role solely for strict provider templates. Use this only for
// transcript presentation/rewind validation, removing only fixed resume framing
// from a compactor's transcript, or skipping manual compaction of exactly this
// one saved message. The model still needs every body fact.
// New summaries are saved exclusively in the model context projection.
func IsLegacyCompactionSummary(msg llm.Message) bool {
	if msg.Role != llm.RoleUser || msg.Name != "" || len(msg.Parts) != 0 || msg.ToolCallID != "" || len(msg.ToolCalls) != 0 {
		return false
	}
	return hasCompactSummaryEnvelope(msg.Content, compactSummaryPreamble, compactSummaryEpilogue) ||
		hasCompactSummaryEnvelope(msg.Content, legacyCompactSummaryPreamble, legacyCompactSummaryEpilogue)
}

func hasCompactSummaryEnvelope(content, preamble, epilogue string) bool {
	if !strings.HasPrefix(content, preamble) || !strings.HasSuffix(content, epilogue) {
		return false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(content, preamble), epilogue)
	return strings.TrimSpace(body) != ""
}
