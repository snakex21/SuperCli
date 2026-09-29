package files

import (
	"bytes"
	"encoding/json"
	"io"
)

// Decode only the relevant object envelope; replacement strings and nested raw
// values stay byte-exact. Duplicate keys must not disappear through a map rewrite.
func uniqueArgumentObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return nil, false
	}
	object := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		key, ok := token.(string)
		if !ok {
			return nil, false
		}
		if _, exists := object[key]; exists {
			return nil, false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, false
		}
		object[key] = value
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return nil, false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, false
	}
	return object, true
}
