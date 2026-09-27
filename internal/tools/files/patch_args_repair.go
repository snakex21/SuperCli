package files

import (
	"bytes"
	"encoding/json"
	"io"
)

// A saved coding turn repeated the root path inside changes[]. The following
// model request only removed that redundant field. Do exactly that, only when
// every supplied nested path equals the explicit root path byte-for-byte.
// Missing roots, competing paths and any ambiguous duplicate keys stay invalid.
func repairRedundantPatchPaths(raw json.RawMessage) (json.RawMessage, bool) {
	object, ok := uniquePatchObject(raw)
	if !ok {
		return nil, false
	}
	var path string
	if json.Unmarshal(object["path"], &path) != nil || path == "" {
		return nil, false
	}
	var changes []json.RawMessage
	if json.Unmarshal(object["changes"], &changes) != nil || len(changes) == 0 {
		return nil, false
	}
	repaired := false
	for i, rawChange := range changes {
		change, ok := uniquePatchObject(rawChange)
		if !ok {
			return nil, false
		}
		nested, exists := change["path"]
		if !exists {
			continue
		}
		var nestedPath string
		if json.Unmarshal(nested, &nestedPath) != nil || nestedPath != path {
			return nil, false
		}
		delete(change, "path")
		encoded, err := json.Marshal(change)
		if err != nil {
			return nil, false
		}
		changes[i] = encoded
		repaired = true
	}
	if !repaired {
		return nil, false
	}
	encoded, err := json.Marshal(changes)
	if err != nil {
		return nil, false
	}
	object["changes"] = encoded
	encoded, err = json.Marshal(object)
	return encoded, err == nil
}

// Decode only the relevant object envelope; replacement strings and nested raw
// values stay byte-exact. Duplicate keys must not disappear through a map rewrite.
func uniquePatchObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
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
