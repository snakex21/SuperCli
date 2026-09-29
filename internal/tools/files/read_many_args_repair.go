package files

import (
	"encoding/json"
	"strings"
)

// Accept reads:["file:from-to", ...] and the equivalent singleton item wrapper.
// Unwrap only a unique string list, preserving every range and its order.
// Keep the compact advertised schema and leave ambiguous formats invalid.
func repairReadManyRangeList(raw json.RawMessage) (json.RawMessage, bool) {
	object, ok := uniqueArgumentObject(raw)
	if !ok || len(object) != 1 {
		return nil, false
	}
	reads, ok := object["reads"]
	if !ok {
		return nil, false
	}
	reads = json.RawMessage(strings.TrimSpace(string(reads)))
	if len(reads) == 0 {
		return nil, false
	}
	if reads[0] == '{' {
		wrapper, ok := uniqueArgumentObject(reads)
		if !ok || len(wrapper) != 1 {
			return nil, false
		}
		reads, ok = wrapper["item"]
		if !ok {
			return nil, false
		}
	}
	var items []string
	if json.Unmarshal(reads, &items) != nil || len(items) == 0 || len(items) > maxReadManyRequests {
		return nil, false
	}
	for _, item := range items {
		// A delimiter inside an element could mean multiple requests or a literal
		// path. Do not reinterpret either, drop fields, or guess missing ranges.
		if strings.TrimSpace(item) == "" || strings.ContainsRune(item, '|') {
			return nil, false
		}
	}
	object["reads"], _ = json.Marshal(strings.Join(items, " | "))
	repaired, err := json.Marshal(object)
	return repaired, err == nil
}
