package agent

import (
	"encoding/json"

	"supercli/internal/llm"
)

const requestedToolContextPreamble = "Requested tools are available for this turn without tool_search. Call them through invoke_tool with tool equal to the name and args matching the full contract below. Normal validation, approvals and safety controls are unchanged."

// Requested contracts stay behind history only when the existing dispatcher is
// advertised. Embedders without it retain the native full-schema capability.
func (l *Loop) usesRequestedToolContext() bool {
	if l.finalReplyOnly || l.route != RouteCoordinator || !l.thinTools || !l.stableToolset || l.registry == nil {
		return false
	}
	_, registered := l.registry.Get(invokeToolName)
	return registered && l.registry.IsVisible(invokeToolName) && l.carriesToolSchema(invokeToolName)
}

func (l *Loop) requestedToolDefinitions() []llm.ToolDef {
	var defs []llm.ToolDef
	for _, requested := range []struct {
		enabled bool
		name    string
	}{
		{l.screenshotForRun, "send_screenshot"},
		{l.headlessForRun, "headless_control"},
		{l.headlessForRun || l.screenshotForRun, "process_session"},
	} {
		if !requested.enabled {
			continue
		}
		if tool, ok := l.registry.Get(requested.name); ok {
			defs = append(defs, llm.ToolDef{Name: tool.Name, Description: tool.Description, Schema: tool.Schema})
		}
	}
	return defs
}

// Read current contracts, including late registrations or registry replacement.
// This request-only text never enters Messages, discovery or the frozen catalog.
func (l *Loop) requestedToolContext() string {
	if !l.usesRequestedToolContext() {
		return ""
	}
	defs := l.requestedToolDefinitions()
	if len(defs) == 0 {
		return ""
	}
	raw, _ := json.Marshal(defs) // ToolDef contains strings only.
	return requestedToolContextPreamble + "\n" + string(raw)
}
