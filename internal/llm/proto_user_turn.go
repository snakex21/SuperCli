package llm

import "strings"

// IsConversationUserTurn identifies an instruction boundary for history
// selection, not proof of human authorship for telemetry or authorization.
// Delegated instructions also start turns. Known host provenance takes
// precedence over report-looking text; old rows retain the legacy fallback.
func IsConversationUserTurn(message Message) bool {
	if message.Role != RoleUser || message.Name == WorkerNotificationName {
		return false
	}
	if message.Origin != nil {
		if source := knownSource(message.Origin.Source); source != ContextUnknown {
			return source == SourceHuman || source == SourceInstructions
		}
		known := false
		for _, span := range message.Origin.Spans {
			if span.Source == SourceHuman || span.Source == SourceInstructions {
				return true
			}
			known = known || knownSource(span.Source) != ContextUnknown
		}
		if known {
			return false
		}
	}
	known := false
	for _, part := range message.Parts {
		if part.Type == PartTypeText {
			if part.Source == SourceHuman || part.Source == SourceInstructions {
				return true
			}
			known = known || knownSource(part.Source) != ContextUnknown
		}
		if part.Image != nil && part.Image.ToolOutputCarrier {
			return false
		}
	}
	if known {
		return false
	}
	// Compatibility only: old persisted tool wrappers lack host provenance.
	// Never use these text heuristics to override a known human instruction.
	if strings.Contains(message.Content, "<task-notification>") {
		return false
	}
	text := strings.TrimSpace(message.Content)
	if text == "" {
		for _, part := range message.Parts {
			if part.Type == PartTypeText {
				text = strings.TrimSpace(part.Text)
				if text != "" {
					break
				}
			}
		}
	}
	return !strings.HasPrefix(text, "Attached image from tool ")
}
