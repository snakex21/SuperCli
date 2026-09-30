package agent

import (
	"strings"

	"supercli/internal/llm"
)

// IsLegacyCompactionSummary recognizes the complete envelope appended to old
// transcripts by CompactPrefixWithSummary. Those rows lack origin metadata and
// used a user role solely for strict provider templates. Use this only for
// transcript presentation/rewind validation; the model still needs the content.
// New summaries are saved exclusively in the model context projection.
func IsLegacyCompactionSummary(msg llm.Message) bool {
	if msg.Role != llm.RoleUser || msg.Name != "" || len(msg.Parts) != 0 || msg.ToolCallID != "" || len(msg.ToolCalls) != 0 {
		return false
	}
	if !strings.HasPrefix(msg.Content, compactSummaryPreamble) || !strings.HasSuffix(msg.Content, compactSummaryEpilogue) {
		return false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(msg.Content, compactSummaryPreamble), compactSummaryEpilogue)
	return strings.TrimSpace(body) != ""
}
