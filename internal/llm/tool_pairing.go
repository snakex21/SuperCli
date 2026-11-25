package llm

import (
	"encoding/json"
	"log"
	"sync/atomic"
)

// MissingToolResultText is the synthetic result of a tool call whose result
// was never saved (interrupted turn, restart between call and result).
const MissingToolResultText = "error: result missing - the call was interrupted before its result was saved"

// toolPairingRepairs counts provider-view repairs (for logs and tests).
var toolPairingRepairs atomic.Int64

// ToolPairingRepairs returns how many tool-pairing repairs request builders
// made in this process.
func ToolPairingRepairs() int64 { return toolPairingRepairs.Load() }

// regroupToolBatches restores the wire order every provider requires before
// repairToolCallIDs validates it. For each assistant tool-call batch, the
// results found before the next assistant message move directly after the
// batch in call order; the messages that sat between them (a worker report
// saved mid-batch, a rewind note) follow the results. A call with no result
// gets a synthetic error result, so a reasoning/thinking block that refers to
// it stays valid. Stray and duplicate results stay where they were and are
// dropped by repairToolCallIDs. Only the provider view changes.
func regroupToolBatches(msgs []Message) ([]Message, int) {
	if !toolBatchesNeedRegroup(msgs) {
		return msgs, 0
	}
	repairs := 0
	out := make([]Message, 0, len(msgs)+4)
	for i := 0; i < len(msgs); {
		m := msgs[i]
		if m.Role != RoleAssistant || len(m.ToolCalls) == 0 {
			out = append(out, m)
			i++
			continue
		}
		end := i + 1
		for end < len(msgs) && msgs[end].Role != RoleAssistant {
			end++
		}
		want := map[string]int{}
		for _, tc := range m.ToolCalls {
			if tc.ID != "" {
				want[tc.ID]++
			}
		}
		results := map[string][]Message{}
		var others []Message
		firstOther, lastResult := -1, -1
		for k := i + 1; k < end; k++ {
			r := msgs[k]
			if r.Role == RoleTool && want[r.ToolCallID] > len(results[r.ToolCallID]) {
				results[r.ToolCallID] = append(results[r.ToolCallID], r)
				lastResult = k
				continue
			}
			if firstOther < 0 {
				firstOther = k
			}
			others = append(others, r)
		}
		if firstOther >= 0 && firstOther < lastResult {
			repairs++ // results moved ahead of interleaved messages
		}
		out = append(out, m)
		used := map[string]int{}
		for _, tc := range m.ToolCalls {
			if tc.ID == "" {
				continue
			}
			if n := used[tc.ID]; n < len(results[tc.ID]) {
				out = append(out, results[tc.ID][n])
				used[tc.ID] = n + 1
				continue
			}
			used[tc.ID]++
			repairs++
			out = append(out, Message{Role: RoleTool, ToolCallID: tc.ID, Name: tc.Name, Content: MissingToolResultText})
		}
		out = append(out, others...)
		i = end
	}
	return out, repairs
}

// toolBatchesNeedRegroup: some batch is not immediately followed by exactly
// its results (in any order).
func toolBatchesNeedRegroup(msgs []Message) bool {
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		if m.Role != RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		pending := map[string]int{}
		for _, tc := range m.ToolCalls {
			pending[tc.ID]++
		}
		j := i + 1
		for j < len(msgs) && msgs[j].Role == RoleTool && pending[msgs[j].ToolCallID] > 0 {
			pending[msgs[j].ToolCallID]--
			j++
		}
		for _, n := range pending {
			if n > 0 {
				return true
			}
		}
	}
	return false
}

// pairToolHistory is the full provider-view repair: regroup, then the strict
// id/structure repair. Repairs are counted and logged (count only).
func pairToolHistory(msgs []Message) []Message {
	msgs, n := regroupToolBatches(msgs)
	if n > 0 {
		toolPairingRepairs.Add(int64(n))
		log.Printf("request repair: %d tool call/result pairing fix(es) in the provider view", n)
	}
	return repairToolCallIDs(msgs)
}

// nativeToolUseIDsMatch reports whether native assistant content blocks carry
// exactly the tool_use ids of the (repaired) message, in order. Replaying
// native blocks with a dropped or renamed call would resend a tool_use whose
// result the request no longer has.
func nativeToolUseIDsMatch(blocks []json.RawMessage, calls []ToolCall) bool {
	var ids []string
	for _, raw := range blocks {
		var b struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if json.Unmarshal(raw, &b) == nil && (b.Type == "tool_use" || b.Type == "server_tool_use") {
			ids = append(ids, b.ID)
		}
	}
	if len(ids) != len(calls) {
		return false
	}
	for i := range ids {
		if ids[i] != calls[i].ID {
			return false
		}
	}
	return true
}
