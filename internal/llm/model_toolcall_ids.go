package llm

import "strconv"

// repairToolCallIDs is the last barrier before a history is turned
// into a provider request. Providers reject the whole request when
// tool-call/result structure is malformed, so this repair happens in
// the provider view only: the stored conversation is left untouched
// and healthy histories stay allocation-free.
//
// Repaired defects:
//
//  1. Duplicate assistant tool-call ids -> renamed uniquely, including
//     duplicates inside one parallel batch. Matching results are renamed too.
//  2. Empty/orphan/out-of-order tool results -> dropped.
//  3. Tool calls without a directly-following result in the same batch ->
//     dropped. If the assistant message then has no text/parts, it is dropped.
//  4. Tool protocol fields on the wrong role -> stripped.
//
// A valid batch is:
//
//	assistant(tool_calls: A,B) -> tool(A) -> tool(B)
//
// Tool results must be the contiguous run immediately following the
// assistant call. This is accepted by strict gateways and prevents a
// compacted/partially hidden history from replaying a stale call/result pair.
func repairToolCallIDs(msgs []Message) []Message {
	if !toolCallHistoryNeedsRepair(msgs) {
		return msgs
	}

	used := make(map[string]bool)
	seq := 0
	freshID := func(base string) string {
		id := base
		for id == "" || used[id] {
			seq++
			if base == "" {
				id = "tool_call_repaired_" + strconv.Itoa(seq)
			} else {
				id = base + "_dup" + strconv.Itoa(seq)
			}
		}
		used[id] = true
		return id
	}

	out := make([]Message, 0, len(msgs))
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]

		// Protocol fields belong to exactly one role. Strip stale fields from
		// old/corrupt rows instead of letting a strict provider reject them.
		if m.Role != RoleAssistant {
			m.ToolCalls = nil
		}
		if m.Role != RoleTool {
			m.ToolCallID = ""
		}

		// A tool result is only legal as part of the contiguous result run
		// consumed below for the immediately preceding assistant tool batch.
		if m.Role == RoleTool {
			continue
		}

		if m.Role != RoleAssistant || len(m.ToolCalls) == 0 {
			if messageHasSendableContent(m) {
				out = append(out, m)
			}
			continue
		}

		// Capture the contiguous tool-result run immediately after this
		// assistant message. Anything later is not a result for this batch.
		j := i + 1
		for j < len(msgs) && msgs[j].Role == RoleTool {
			j++
		}
		results := msgs[i+1 : j]

		// Queue result positions by original id. A queue (not a single map
		// entry) is essential for parallel calls that accidentally reuse an id.
		resultQueues := make(map[string][]int)
		for ri, r := range results {
			if r.ToolCallID != "" {
				resultQueues[r.ToolCallID] = append(resultQueues[r.ToolCallID], ri)
			}
		}
		resultCursor := make(map[string]int)
		matchedResultID := make(map[int]string)
		calls := make([]ToolCall, 0, len(m.ToolCalls))

		for _, tc := range m.ToolCalls {
			if tc.ID == "" {
				continue
			}
			queue := resultQueues[tc.ID]
			cursor := resultCursor[tc.ID]
			if cursor >= len(queue) {
				continue // no directly-following result for this call
			}
			ri := queue[cursor]
			resultCursor[tc.ID] = cursor + 1

			id := freshID(tc.ID)
			tc.ID = id
			calls = append(calls, tc)
			matchedResultID[ri] = id
		}

		m.ToolCalls = calls
		if messageHasSendableContent(m) {
			out = append(out, m)
		}
		for ri, r := range results {
			id, ok := matchedResultID[ri]
			if !ok {
				continue
			}
			r.ToolCalls = nil
			r.ToolCallID = id
			out = append(out, r)
		}
		i = j - 1
	}
	return out
}

func messageHasSendableContent(m Message) bool {
	if m.Role == RoleTool {
		return m.ToolCallID != ""
	}
	return messageText(m) != "" || len(m.Parts) > 0 || (m.Role == RoleAssistant && len(m.ToolCalls) > 0)
}

// toolCallHistoryNeedsRepair is the allocation-saving fast path. It validates
// the exact wire invariant rather than merely checking that a matching id
// exists somewhere in history.
func toolCallHistoryNeedsRepair(msgs []Message) bool {
	seen := make(map[string]bool)
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]

		if m.Role != RoleAssistant && len(m.ToolCalls) > 0 {
			return true
		}
		if m.Role != RoleTool && m.ToolCallID != "" {
			return true
		}
		if m.Role == RoleTool {
			return true // not consumed as part of a preceding valid batch
		}
		if m.Role != RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}

		need := make(map[string]int, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			if tc.ID == "" || seen[tc.ID] {
				return true
			}
			seen[tc.ID] = true
			need[tc.ID]++
		}

		j := i + 1
		got := 0
		for j < len(msgs) && msgs[j].Role == RoleTool {
			r := msgs[j]
			if r.ToolCallID == "" || len(r.ToolCalls) > 0 || need[r.ToolCallID] == 0 {
				return true
			}
			need[r.ToolCallID]--
			got++
			j++
		}
		if got != len(m.ToolCalls) {
			return true
		}
		for _, n := range need {
			if n != 0 {
				return true
			}
		}
		i = j - 1
	}
	return false
}
