package llm

import (
	"strings"
	"unicode"
)

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
		out.Tool += estimateToolDefinitionTokens(tool)
	}
	return out
}

// Count the same text as TrimSpace(name + " " + description + " " + schema)
// without allocating a combined string for every definition on every estimate.
// Trimming only the outer edges preserves interior Unicode whitespace exactly.
func estimateToolDefinitionTokens(tool ToolDef) int {
	parts := [3]string{tool.Name, tool.Description, tool.Schema}
	for i := 0; i < len(parts); i++ {
		parts[i] = strings.TrimLeftFunc(parts[i], unicode.IsSpace)
		if parts[i] != "" {
			break
		}
	}
	for i := len(parts) - 1; i >= 0; i-- {
		parts[i] = strings.TrimRightFunc(parts[i], unicode.IsSpace)
		if parts[i] != "" {
			break
		}
	}
	if parts[0] == "" && parts[1] == "" && parts[2] == "" {
		return 0
	}
	bytes := 0
	for _, part := range parts {
		bytes += nonWhitespaceLen(part)
	}
	return bytes/estBytesPerToken + estPerMessageCost
}
