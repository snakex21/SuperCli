package agent

import (
	"unicode/utf8"

	"supercli/internal/llm"
)

// sharedWorkerSeed is equivalent to filtering system messages into a fresh
// slice and then, for advisors, applying advisorSeed. The returned message slice
// is independent of the parent; nested payloads retain the old shallow-copy
// ownership contract. Callers decide whether the worker uses advisor policy.
func sharedWorkerSeed(messages []llm.Message, advisor bool) []llm.Message {
	count := 0
	for _, message := range messages {
		if message.Role != llm.RoleSystem {
			count++
		}
	}
	if !advisor {
		if count == 0 {
			return nil
		}
		out := make([]llm.Message, 0, count)
		for _, message := range messages {
			if message.Role != llm.RoleSystem {
				out = append(out, message)
			}
		}
		return out
	}

	// Locate the same tail in the filtered sequence without constructing it.
	skip := count - advisorSeedMessages
	if skip < 0 {
		skip = 0
	}
	start := 0
	for start < len(messages) {
		role := messages[start].Role
		if role == llm.RoleSystem {
			start++
			continue
		}
		if skip == 0 {
			break
		}
		skip--
		start++
	}
	// Exactly as advisorSeed: skip only leading tool results, not interior ones.
	for start < len(messages) && (messages[start].Role == llm.RoleSystem || messages[start].Role == llm.RoleTool) {
		start++
	}
	out := make([]llm.Message, 0, advisorSeedMessages+1)
	for i := len(messages) - 1; i >= 0; i-- {
		if isConversationUserTurn(messages[i]) {
			if i < start {
				out = append(out, messages[i])
			}
			break
		}
	}
	for _, message := range messages[start:] {
		if message.Role == llm.RoleSystem {
			continue
		}
		if message.Role == llm.RoleTool && len(message.Parts) == 0 && len(message.Content) > advisorSeedToolBytes {
			cut := advisorSeedToolBytes
			for cut > 0 && !utf8.RuneStart(message.Content[cut]) {
				cut--
			}
			message.Content = message.Content[:cut] + "\n[… clipped for advisor]"
		}
		out = append(out, message)
	}
	return out
}
