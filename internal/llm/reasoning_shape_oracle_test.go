package llm

import (
	"encoding/json"
	"sort"
	"strings"
)

// reasoningShapeOracleExtractStringLeaves preserves the recursive decoder before
// shape dispatch. It is independent of the production recursion for differential tests.
func reasoningShapeOracleExtractStringLeaves(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		var out []string
		for _, item := range arr {
			if v := reasoningShapeOracleExtractStringLeaves(item); v != "" {
				out = append(out, v)
			}
		}
		return strings.Join(out, "")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		// Text-bearing keys first. When any of them yields text, that IS
		// the reasoning: the remaining keys are metadata (type, format,
		// index, …) and must never leak into the visible stream — that is
		// how literal "reasoning.text"/"unknown" ended up interleaved
		// with the model's thinking in the GUI.
		preferred := []string{"text", "content", "value", "delta", "thinking", "reasoning"}
		var out []string
		for _, key := range preferred {
			if v, ok := obj[key]; ok {
				if s := reasoningShapeOracleExtractStringLeaves(v); s != "" {
					out = append(out, s)
				}
			}
		}
		if len(out) > 0 {
			return strings.Join(out, "")
		}
		// No known text key: walk the rest deterministically, skipping
		// anything metadata-shaped.
		keys := make([]string, 0, len(obj))
		for key := range obj {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if isReasoningMetadataKey(key) {
				continue
			}
			if s := reasoningShapeOracleExtractStringLeaves(obj[key]); s != "" {
				out = append(out, s)
			}
		}
		return strings.Join(out, "")
	}
	return ""
}
