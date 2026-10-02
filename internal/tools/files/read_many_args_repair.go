package files

import (
	"encoding/json"
	"strings"
)

// Accept a string list or a singleton item wrapper containing a list/shorthand.
// Unwrap only an unambiguous representation, preserving every range and its order.
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
		// A wrapped shorthand is already the complete reads format. Keep its
		// literal value; the normal parser still handles delimiters and caps.
		// uniqueArgumentObject decodes each RawMessage after JSON whitespace.
		// Gate the type so accepted string lists avoid a failed string decode.
		if len(reads) > 0 && reads[0] == '"' {
			var shorthand string
			if json.Unmarshal(reads, &shorthand) != nil || strings.TrimSpace(shorthand) == "" {
				return nil, false
			}
			object["reads"] = reads
			repaired, err := json.Marshal(object)
			return repaired, err == nil
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
