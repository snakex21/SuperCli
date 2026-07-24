package llm

import "strings"

// RequestBreakdown is a local, estimator-based split of one request's
// prompt tokens by role. It is attached to every CallStat so per-call
// sinks (the web GUI's usage rows) can show WHERE the context weight
// sits without re-parsing — or ever seeing — the prompt.
type RequestBreakdown struct {
	System    int
	User      int
	Assistant int
	Tool      int
	Other     int
}

// EstimateRequestBreakdown estimates the request's token weight per
// role using the same calibrated estimator the context-defense code
// uses. Tool CALLS inside assistant messages and the tool-definition
// block are attributed to Tool, mirroring how backends bill them.
func EstimateRequestBreakdown(msgs []Message, tools []ToolDef) RequestBreakdown {
	var out RequestBreakdown
	for _, msg := range msgs {
		tokens := EstimateMessageTokens(msg)
		switch msg.Role {
		case RoleSystem:
			out.System += tokens
		case RoleUser:
			out.User += tokens
		case RoleTool:
			out.Tool += tokens
		case RoleAssistant:
			toolTokens := 0
			if len(msg.ToolCalls) > 0 {
				toolOnly := Message{Role: RoleAssistant, ToolCalls: msg.ToolCalls}
				toolTokens = EstimateMessageTokens(toolOnly) - 16
				if toolTokens < 0 {
					toolTokens = 0
				}
				if toolTokens > tokens {
					toolTokens = tokens
				}
			}
			out.Tool += toolTokens
			out.Assistant += tokens - toolTokens
		default:
			out.Other += tokens
		}
	}
	for _, tool := range tools {
		text := strings.TrimSpace(tool.Name + " " + tool.Description + " " + tool.Schema)
		if text != "" {
			out.Tool += EstimateMessageTokens(Message{Role: RoleSystem, Content: text})
		}
	}
	return out
}
