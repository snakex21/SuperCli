package agent

import (
	"crypto/sha256"
	"encoding/json"

	"supercli/internal/llm"
)

// A model can emit the same read as both a native call and a text protocol
// block in one response. Keep the native call/ID and remove only its text
// mirror, before either execution or history storage. This is not a cache
// across responses, and all native calls remain intact.
func (l *Loop) coalesceMirroredReads(calls []llm.ToolCall, textIndexes []int) []llm.ToolCall {
	if len(textIndexes) == 0 || len(textIndexes) == len(calls) || l.registry == nil {
		return calls
	}
	text := make([]bool, len(calls))
	for _, index := range textIndexes {
		text[index] = true
	}
	keys := make([][sha256.Size]byte, len(calls))
	safe := make([]bool, len(calls))
	for i, call := range calls {
		keys[i], safe[i] = l.registry.ReadOnlyCallKey(call.Name, json.RawMessage(call.Arguments))
	}
	out := calls[:0]
	for start := 0; start < len(calls); {
		if !safe[start] {
			out = append(out, calls[start])
			start++
			continue
		}
		// A mutation, unknown tool or invalid call is a barrier: a later read
		// may legitimately observe changed state. Compare only a read-only run.
		end := start
		native := make(map[[sha256.Size]byte]int)
		for end < len(calls) && safe[end] {
			if !text[end] {
				native[keys[end]]++
			}
			end++
		}
		for i := start; i < end; i++ {
			if text[i] && native[keys[i]] > 0 {
				native[keys[i]]--
				continue
			}
			out = append(out, calls[i])
		}
		start = end
	}
	return out
}
