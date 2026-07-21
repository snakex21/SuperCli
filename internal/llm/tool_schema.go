package llm

import (
	"encoding/json"
	"reflect"
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

// normalizeToolSchemaForModel applies compatibility rules required by model
// families that accept OpenAI-style tools but validate a different JSON Schema
// dialect. Keep these rewrites model-scoped so stricter OpenAI-compatible
// providers continue to receive the conventional object root.
func normalizeToolSchemaForModel(raw, model string) json.RawMessage {
	schema := normalizeToolSchema(raw)
	if !usesMoonshotToolSchema(model) {
		return schema
	}

	var value any
	if err := json.Unmarshal(schema, &value); err != nil {
		return schema
	}
	rewriteMoonshotAnyOfTypes(value, true)
	return mustMarshalSchema(value)
}

func usesMoonshotToolSchema(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(model, "kimi") || strings.Contains(model, "moonshot")
}

// Moonshot's tool-schema validator rejects a type declared beside anyOf and
// requires that type to be present in every anyOf branch. Tool parameters also
// have a separate requirement that their root must be type=object, making a
// root anyOf impossible to express in that dialect. Root union constraints are
// therefore dropped (tool execution still validates the arguments), while
// nested unions have the parent type moved into their branches.
func rewriteMoonshotAnyOfTypes(value any, root bool) {
	switch node := value.(type) {
	case map[string]any:
		if root {
			if _, hasAnyOf := node["anyOf"]; hasAnyOf {
				delete(node, "anyOf")
				node["type"] = "object"
			}
		}
		if parentType, hasType := node["type"]; hasType {
			if branches, ok := node["anyOf"].([]any); !root && ok && len(branches) > 0 {
				branchObjects := make([]map[string]any, 0, len(branches))
				compatible := true
				for _, branch := range branches {
					branchObject, ok := branch.(map[string]any)
					if !ok {
						compatible = false
						break
					}
					if branchType, exists := branchObject["type"]; exists && !reflect.DeepEqual(branchType, parentType) {
						compatible = false
						break
					}
					branchObjects = append(branchObjects, branchObject)
				}
				if compatible {
					for _, branch := range branchObjects {
						if _, exists := branch["type"]; !exists {
							branch["type"] = parentType
						}
					}
					delete(node, "type")
				}
			}
		}
		for _, child := range node {
			rewriteMoonshotAnyOfTypes(child, false)
		}
	case []any:
		for _, child := range node {
			rewriteMoonshotAnyOfTypes(child, false)
		}
	}
}

func mustMarshalSchema(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return emptyObjectSchema
	}
	return json.RawMessage(b)
}
