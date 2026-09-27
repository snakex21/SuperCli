package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
)

// ReadOnlyCallKey compares calls using the same coercion and validation as
// Execute. Unknown, mutating or invalid calls are never eligible for coalescing.
func (r *Registry) ReadOnlyCallKey(name string, args json.RawMessage) ([sha256.Size]byte, bool) {
	r.mu.RLock()
	tool, ok := r.tools[name]
	schema := r.schemas[name]
	r.mu.RUnlock()
	if !ok || !tool.ReadOnly {
		return [sha256.Size]byte{}, false
	}
	args = coerceCompiledArgs(schema, args)
	if schema != nil {
		if err := schema.validateJSON(args); err != nil {
			return [sha256.Size]byte{}, false
		}
	}
	if !json.Valid(args) {
		return [sha256.Size]byte{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.UseNumber() // Distinct large integer offsets must not collapse via float64.
	var object map[string]any
	if decoder.Decode(&object) != nil || object == nil {
		return [sha256.Size]byte{}, false
	}
	encoded, err := json.Marshal(struct {
		Name string
		Args map[string]any
	}{name, object})
	if err != nil {
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256(encoded), true
}
