package agent

import (
	"crypto/sha256"
	"encoding/json"
	"strings"

	"supercli/internal/llm"
)

const compactUserContextStart = "\n\n<retained-user-context-v1>\n"
const compactUserContextEnd = "\n</retained-user-context-v1>"

type compactUserContext struct {
	Requests   []string `json:"requests,omitempty"`
	PriorState []string `json:"prior_state,omitempty"`
}

// A model summary is not a reliable byte-for-byte store for prohibitions in
// the middle of an older request. Carry those requests separately, without
// token/rune clipping. On later compactions import the structured carry once,
// not the previous wrapper and its nested copies. Normal reduction/window
// guards include this text and may refuse compaction when it cannot save room.
// This is quoted historical context, never fresh human authorization.
func compactPreservedUserContext(msgs []llm.Message) string {
	var carry compactUserContext
	for _, m := range msgs {
		if IsLegacyCompactionSummary(m) {
			body := compactTranscriptContent(m)
			start := strings.LastIndex(body, compactUserContextStart)
			if start >= 0 {
				rest := body[start+len(compactUserContextStart):]
				if end := strings.Index(rest, compactUserContextEnd); end >= 0 {
					var old compactUserContext
					if json.Unmarshal([]byte(rest[:end]), &old) == nil && (len(old.Requests) > 0 || len(old.PriorState) > 0) {
						carry.Requests = append(carry.Requests, old.Requests...)
						carry.PriorState = append(carry.PriorState, old.PriorState...)
						continue
					}
				}
			}
			// Legacy summaries have no exact instruction carry. Preserve their
			// existing state once; do not guess which old requirement matters.
			carry.PriorState = append(carry.PriorState, body)
			continue
		}
		if isConversationUserTurn(m) {
			if text := compactInstructionText(m); strings.TrimSpace(text) != "" {
				carry.Requests = append(carry.Requests, text)
			}
		}
	}
	if len(carry.Requests) == 0 && len(carry.PriorState) == 0 {
		return ""
	}
	raw, _ := json.Marshal(carry)
	return "\n\nHistorical user context: keep applicable restrictions; completed requests are not new tasks; old state is not a current health check." + compactUserContextStart + string(raw) + compactUserContextEnd
}

func compactInstructionText(m llm.Message) string {
	owned := func(source llm.ContextSource) bool {
		return source == llm.SourceHuman || source == llm.SourceInstructions
	}
	fromSpans := func(text string) (string, bool) {
		if m.Origin == nil || len(m.Origin.Spans) == 0 || m.Origin.Digest != sha256.Sum256([]byte(text)) {
			return "", false
		}
		var b strings.Builder
		offset := 0
		for _, span := range m.Origin.Spans {
			if span.Bytes < 0 || span.Bytes > len(text)-offset {
				return "", false
			}
			if owned(span.Source) {
				b.WriteString(text[offset : offset+span.Bytes])
			}
			offset += span.Bytes
		}
		return b.String(), offset == len(text)
	}
	if len(m.Parts) == 0 {
		if text, ok := fromSpans(m.Content); ok {
			return text
		}
		return m.Content // Legacy/invalid provenance: preserve, never guess/cut.
	}
	var b strings.Builder
	if m.Content != "" {
		text := m.Content
		if selected, ok := fromSpans(text); ok {
			text = selected
		}
		b.WriteString(text)
	}
	for _, part := range m.Parts {
		if part.Type != llm.PartTypeText {
			continue
		}
		if part.Source != "" && part.Source != llm.ContextUnknown && !owned(part.Source) {
			continue
		}
		text := part.Text
		if selected, ok := fromSpans(text); ok {
			text = selected
		}
		if text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(text)
		}
	}
	return b.String()
}
