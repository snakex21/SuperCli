package llm

import (
	"encoding/json"
	"strings"
)

var emptyObjectSchema = json.RawMessage(`{"type":"object","properties":{}}`)

// normalizeToolSchema converts SuperCli's historical shorthand tool schemas
// into full JSON Schema objects accepted by OpenAI-compatible APIs.
//
// Older core tools often define Schema as only the properties object, e.g.:
//
//	{"file":{"type":"string"},"from":{"type":"integer"}}
//
// The APIs expect the parameters value itself to be a JSON Schema whose root is
// an object, e.g.:
//
//	{"type":"object","properties":{"file":{...},"from":{...}}}
//
// If the schema is already a complete schema, it is preserved except that a
// missing/null root type is filled with "object".
func normalizeToolSchema(raw string) json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return emptyObjectSchema
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil || obj == nil {
		return emptyObjectSchema
	}

	if _, hasType := obj["type"]; hasType {
		if obj["type"] == nil {
			obj["type"] = "object"
		}
		if obj["type"] == "object" {
			if _, ok := obj["properties"]; !ok {
				obj["properties"] = map[string]any{}
			}
		}
		return mustMarshalSchema(obj)
	}

	if len(obj) == 0 {
		return emptyObjectSchema
	}

	// Schema-shaped but missing a root type.
	if _, ok := obj["properties"]; ok {
		obj["type"] = "object"
		return mustMarshalSchema(obj)
	}
	if _, ok := obj["required"]; ok {
		obj["type"] = "object"
		if _, hasProps := obj["properties"]; !hasProps {
			obj["properties"] = map[string]any{}
		}
		return mustMarshalSchema(obj)
	}
	if _, ok := obj["additionalProperties"]; ok {
		obj["type"] = "object"
		if _, hasProps := obj["properties"]; !hasProps {
			obj["properties"] = map[string]any{}
		}
		return mustMarshalSchema(obj)
	}

	// Historical shorthand: the root object is the properties map.
	return mustMarshalSchema(map[string]any{
		"type":       "object",
		"properties": obj,
	})
}

func mustMarshalSchema(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return emptyObjectSchema
	}
	return json.RawMessage(b)
}
