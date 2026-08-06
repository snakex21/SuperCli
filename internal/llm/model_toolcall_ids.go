package llm

import "strconv"

// repairToolCallIDs is the last barrier before a history is turned
// into a provider request. Providers reject the whole request when
// the conversation contains two tool calls with the same id
// ("Duplicate value for 'tool_call_id' of X in message[73]"), or a
// tool result that answers no tool call. Both are fatal HTTP 400s
// that lose the user's entire session, so the repair happens here —
// in the one place every provider path goes through — instead of
// being re-implemented per caller (TUI, WebGUI, batch, worker).
//
// Three defects are repaired, in history order:
//
//  1. Duplicate / empty assistant tool-call id → renamed to a fresh
//     unique id. The tool result that answers it is renamed too, so
//     the pairing survives.
//  2. Orphan tool result (ToolCallID matches no earlier tool call) →
//     dropped. There is nothing it can be attached to.
//  3. Orphan tool call (no tool result anywhere after it) → dropped
//     from the assistant message; if that leaves the message empty,
//     the message is dropped as well.
//
// A history that is already well-formed is returned unchanged (no
// allocation), so the common path costs one scan.
func repairToolCallIDs(msgs []Message) []Message {
	if !toolCallHistoryNeedsRepair(msgs) {
		return msgs
	}

	// answered[id] = a tool result exists for this exact id.
	answered := make(map[string]bool)
	for _, m := range msgs {
		if m.Role == RoleTool && m.ToolCallID != "" {
			answered[m.ToolCallID] = true
		}
	}

	used := make(map[string]bool)      // ids already emitted
	binding := make(map[string]string) // original id → id actually emitted
	seq := 0

	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		switch {
		case m.Role == RoleTool:
			// Rewrite to whatever the matching call was renamed to.
			id, ok := binding[m.ToolCallID]
			if !ok {
				continue // orphan result: nothing to answer
			}
			delete(binding, m.ToolCallID) // one result per call
			m.ToolCallID = id
			out = append(out, m)

		case len(m.ToolCalls) > 0:
			calls := make([]ToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				if tc.ID == "" || !answered[tc.ID] {
					continue // orphan call: no result will follow
				}
				id := tc.ID
				for used[id] {
					seq++
					id = tc.ID + "_dup" + strconv.Itoa(seq)
				}
				used[id] = true
				binding[tc.ID] = id
				tc.ID = id
				calls = append(calls, tc)
			}
			if len(calls) == 0 && messageText(m) == "" && len(m.Parts) == 0 {
				continue // nothing left to send
			}
			m.ToolCalls = calls
			out = append(out, m)

		default:
			out = append(out, m)
		}
	}
	return out
}

// toolCallHistoryNeedsRepair reports whether any of the three
// defects is present. Cheap scan on the healthy path.
func toolCallHistoryNeedsRepair(msgs []Message) bool {
	answered := make(map[string]int)
	for _, m := range msgs {
		if m.Role == RoleTool {
			if m.ToolCallID == "" {
				return true
			}
			answered[m.ToolCallID]++
		}
	}
	seen := make(map[string]bool)
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			if tc.ID == "" || seen[tc.ID] || answered[tc.ID] == 0 {
				return true
			}
			seen[tc.ID] = true
			answered[tc.ID]--
		}
	}
	// Any leftover result answers no call.
	for _, n := range answered {
		if n != 0 {
			return true
		}
	}
	return false
}
