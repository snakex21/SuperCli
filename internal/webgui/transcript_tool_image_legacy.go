package webgui

import (
	"strings"

	"supercli/internal/llm"
)

func isTranscriptToolImageCarrier(msg llm.Message, realAttachments bool) bool {
	if realAttachments || msg.Role != llm.RoleUser || msg.Content != "" || msg.Name != "" || msg.ToolCallID != "" || len(msg.ToolCalls) != 0 {
		return false
	}
	found := false
	for _, part := range msg.Parts {
		switch part.Type {
		case llm.PartTypeText:
		case llm.PartTypeImage:
			if part.Image == nil || !part.Image.ToolOutputCarrier || part.Image.SourceToolCallID == "" {
				return false
			}
			found = true
		default:
			return false
		}
	}
	return found
}

// Pre-provenance versions wrote this exact internal envelope. Words alone are
// not sufficient: also require host image names, portable session-media paths
// and a unique matching assistant call with its immediately preceding result.
func legacyToolImageName(msg llm.Message, realAttachments bool) string {
	if realAttachments || msg.Role != llm.RoleUser || msg.Content != "" || msg.Name != "" || msg.ToolCallID != "" || len(msg.ToolCalls) != 0 || len(msg.Parts) < 2 || msg.Parts[0].Type != llm.PartTypeText {
		return ""
	}
	const prefix = "Attached image from tool "
	text := msg.Parts[0].Text
	if !strings.HasPrefix(text, prefix) || !strings.HasSuffix(text, ":") {
		return ""
	}
	name := strings.TrimSuffix(strings.TrimPrefix(text, prefix), ":")
	if name == "" || strings.ContainsAny(name, " \t\r\n") {
		return ""
	}
	for _, part := range msg.Parts[1:] {
		if part.Type != llm.PartTypeImage || part.Image == nil || part.Image.ToolOutputCarrier || part.Image.SourceToolCallID != "" || part.Image.Name != "tool "+name || sessionImagePreviewPath("validation", part.Image.Path) == "" {
			return ""
		}
	}
	return name
}

func legacyToolImageNeedsContext(prior []llm.Message) bool {
	for i := len(prior) - 1; i >= 0; i-- {
		if prior[i].Role != llm.RoleTool {
			return false
		}
	}
	return true
}

func projectLegacyToolImage(msg llm.Message, prior []llm.Message) llm.Message {
	name := legacyToolImageName(msg, false)
	if name == "" {
		return msg
	}
	results := make(map[string]string)
	i := len(prior) - 1
	for i >= 0 && prior[i].Role == llm.RoleTool {
		results[prior[i].ToolCallID] = prior[i].Name
		i--
	}
	if i < 0 || prior[i].Role != llm.RoleAssistant {
		return msg
	}
	sourceID := ""
	for _, call := range prior[i].ToolCalls {
		resultName, found := results[call.ID]
		if call.Name == name && found && (resultName == "" || resultName == name) {
			if sourceID != "" {
				return msg
			} // never guess among identical parallel tools
			sourceID = call.ID
		}
	}
	if sourceID == "" {
		return msg
	}
	msg.Parts = append([]llm.ContentPart(nil), msg.Parts...)
	for index := 1; index < len(msg.Parts); index++ {
		ref := *msg.Parts[index].Image
		ref.ToolOutputCarrier, ref.SourceToolCallID = true, sourceID
		msg.Parts[index].Image = &ref
	}
	return msg
}
