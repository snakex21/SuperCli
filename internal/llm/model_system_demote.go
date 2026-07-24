package llm

import "strings"

// demoteMidConversationSystemMessages keeps only the LEADING run of
// system messages as RoleSystem. Every system message that appears
// after the first non-system message (freshness stamps, thin-tools
// preambles, reflection checkpoints, Sisyphus reminders, compaction
// summaries) is re-rendered IN PLACE, in history order, as a user
// message wrapped in <system-reminder> tags — a convention chat models
// are widely trained on.
//
// Why: providers used to hoist all system text into the leading system
// block. That moved per-request volatile content (the minute-granular
// freshness stamp) to the FRONT of the prompt, so every minute tick —
// and every mid-history system injection — invalidated the entire
// server-side KV cache (measured: full re-eval of the context instead
// of a few dozen tokens). Keeping late system content at its original
// position preserves the append-only prompt prefix.
//
// To stay safe for strict alternating chat templates, a demoted
// reminder is merged into an adjacent plain-text user message instead
// of producing two consecutive user turns: it is appended to a
// directly preceding user message, and a user message directly
// following a demoted reminder is folded into it. Both merges only
// append bytes at the current tail of the prompt, so the cacheable
// prefix built on earlier turns is untouched.
func demoteMidConversationSystemMessages(msgs []Message) []Message {
	first := 0
	for first < len(msgs) && msgs[first].Role == RoleSystem {
		first++
	}
	needsRewrite := false
	for _, m := range msgs[first:] {
		if m.Role == RoleSystem {
			needsRewrite = true
			break
		}
	}
	if !needsRewrite {
		return msgs
	}

	out := make([]Message, 0, len(msgs))
	out = append(out, msgs[:first]...)
	lastDemoted := false
	for _, m := range msgs[first:] {
		if m.Role == RoleSystem {
			text := strings.TrimSpace(messageText(m))
			if text == "" {
				continue
			}
			reminder := "<system-reminder>\n" + text + "\n</system-reminder>"
			if n := len(out); n > 0 && mergeableUser(out[n-1]) {
				out[n-1].Content = out[n-1].Content + "\n\n" + reminder
			} else {
				out = append(out, Message{Role: RoleUser, Content: reminder})
			}
			lastDemoted = true
			continue
		}
		if lastDemoted && mergeableUser(m) {
			// A real user message right after a demoted reminder:
			// fold it in so templates never see user, user.
			n := len(out)
			out[n-1].Content = out[n-1].Content + "\n\n" + m.Content
			lastDemoted = false
			continue
		}
		lastDemoted = false
		out = append(out, m)
	}
	return out
}

// mergeableUser reports whether m is a plain-text user message that a
// demoted system reminder can be safely concatenated with.
func mergeableUser(m Message) bool {
	return m.Role == RoleUser && len(m.Parts) == 0 && m.ToolCallID == "" && m.Name == "" && len(m.ToolCalls) == 0
}
