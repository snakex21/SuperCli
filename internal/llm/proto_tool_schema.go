package llm

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

var emptyObjectSchema = json.RawMessage(`{"type":"object","properties":{}}`)

const toolSchemaCacheLimit = 512

type toolSchemaMode uint8

const (
	toolSchemaFull toolSchemaMode = iota
	toolSchemaPortable
)

type toolSchemaCacheKey struct {
	mode toolSchemaMode
	raw  string
}

var normalizedToolSchemaCache = struct {
	sync.Mutex
	entries map[toolSchemaCacheKey]json.RawMessage
	order   []toolSchemaCacheKey
}{entries: make(map[toolSchemaCacheKey]json.RawMessage)}

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
	schema, err := normalizeToolSchemaChecked(raw)
	if err != nil {
		return append(json.RawMessage(nil), emptyObjectSchema...)
	}
	return schema
}

// normalizeToolSchemaChecked compiles the complete local schema. Provider
// request builders use this checked form so malformed tool definitions cannot
// silently turn into an unconstrained empty object.
func normalizeToolSchemaChecked(raw string) (json.RawMessage, error) {
	return cachedToolSchema(raw, toolSchemaFull)
}

func normalizedToolSchemaObject(raw string) map[string]any {
	obj, err := normalizedToolSchemaObjectChecked(raw)
	if err != nil {
		return newEmptyObjectSchema()
	}
	return obj
}

func normalizedToolSchemaObjectChecked(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return newEmptyObjectSchema(), nil
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, fmt.Errorf("invalid JSON schema: %w", err)
	}
	if obj == nil {
		return nil, fmt.Errorf("invalid JSON schema: root must be an object")
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
		return obj, nil
	}

	if len(obj) == 0 {
		return newEmptyObjectSchema(), nil
	}

	// Schema-shaped but missing a root type.
	if _, ok := obj["properties"]; ok {
		obj["type"] = "object"
		return obj, nil
	}
	if _, ok := obj["required"]; ok {
		obj["type"] = "object"
		if _, hasProps := obj["properties"]; !hasProps {
			obj["properties"] = map[string]any{}
		}
		return obj, nil
	}
	if _, ok := obj["additionalProperties"]; ok {
		obj["type"] = "object"
		if _, hasProps := obj["properties"]; !hasProps {
			obj["properties"] = map[string]any{}
		}
		return obj, nil
	}
	if schemaHasKnownKeyword(obj) {
		obj["type"] = "object"
		return obj, nil
	}

	// Historical shorthand: the root object is the properties map.
	return map[string]any{
		"type":       "object",
		"properties": obj,
	}, nil
}

func newEmptyObjectSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

// normalizeOpenAIToolSchema emits the conservative JSON-Schema subset shared
// by OpenAI-compatible clouds, local servers and resellers. Doing this before
// the request avoids provider probing and a failed compatibility round-trip.
// The original ToolDef.Schema is not mutated, so the execution layer can use
// the complete schema for local validation independently of this wire form.
func normalizeOpenAIToolSchema(raw string) json.RawMessage {
	schema, err := normalizeOpenAIToolSchemaChecked(raw)
	if err != nil {
		return append(json.RawMessage(nil), emptyObjectSchema...)
	}
	return schema
}

// normalizeOpenAIToolSchemaChecked compiles the conservative wire schema used
// for OpenAI-compatible APIs. The original ToolDef.Schema remains untouched
// and available to the local execution layer for full validation.
func normalizeOpenAIToolSchemaChecked(raw string) (json.RawMessage, error) {
	return cachedToolSchema(raw, toolSchemaPortable)
}

// CompiledToolSchema keeps the complete schema used for local argument
// validation separate from the compatibility form sent over the wire.
type CompiledToolSchema struct {
	Full     json.RawMessage
	Portable json.RawMessage
}

// CompileToolSchema prepares both immutable transport forms for callers that
// need them together. Execution keeps its complete compiled validator in the
// tools registry, so wire compatibility never weakens local argument checks.
func CompileToolSchema(raw string) (CompiledToolSchema, error) {
	full, err := normalizeToolSchemaChecked(raw)
	if err != nil {
		return CompiledToolSchema{}, err
	}
	portable, err := normalizeOpenAIToolSchemaChecked(raw)
	if err != nil {
		return CompiledToolSchema{}, err
	}
	return CompiledToolSchema{Full: full, Portable: portable}, nil
}

func cachedToolSchema(raw string, mode toolSchemaMode) (json.RawMessage, error) {
	raw = strings.TrimSpace(raw)
	key := toolSchemaCacheKey{mode: mode, raw: raw}
	normalizedToolSchemaCache.Lock()
	if cached, ok := normalizedToolSchemaCache.entries[key]; ok {
		out := append(json.RawMessage(nil), cached...)
		normalizedToolSchemaCache.Unlock()
		return out, nil
	}
	normalizedToolSchemaCache.Unlock()

	schema, err := normalizedToolSchemaObjectChecked(raw)
	if err != nil {
		return nil, err
	}
	if mode == toolSchemaPortable {
		rewritePortableToolSchema(schema, true)
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal JSON schema: %w", err)
	}
	value := json.RawMessage(encoded)

	normalizedToolSchemaCache.Lock()
	if _, exists := normalizedToolSchemaCache.entries[key]; !exists {
		if len(normalizedToolSchemaCache.order) >= toolSchemaCacheLimit {
			oldest := normalizedToolSchemaCache.order[0]
			delete(normalizedToolSchemaCache.entries, oldest)
			copy(normalizedToolSchemaCache.order, normalizedToolSchemaCache.order[1:])
			normalizedToolSchemaCache.order = normalizedToolSchemaCache.order[:len(normalizedToolSchemaCache.order)-1]
		}
		normalizedToolSchemaCache.entries[key] = append(json.RawMessage(nil), value...)
		normalizedToolSchemaCache.order = append(normalizedToolSchemaCache.order, key)
	}
	normalizedToolSchemaCache.Unlock()
	return append(json.RawMessage(nil), value...), nil
}

// Some OpenAI-compatible validators use a narrower dialect than the official
// endpoint:
//   - a type beside anyOf is rejected; it must live in the branches;
//   - every schema that describes an object must say type=object explicitly;
//   - the parameters root must always be type=object.
//
// Tool schemas can come from built-ins, skills and MCP, so this must walk the
// complete schema tree. It deliberately follows JSON-Schema child keywords
// rather than every map value: the map stored under "properties" is a property
// name table, not itself a schema (adding a "type" entry there would create a
// bogus argument named "type").
func rewritePortableToolSchema(value any, root bool) {
	node, ok := value.(map[string]any)
	if !ok {
		return
	}

	if root {
		// Reseller validators can simultaneously require an object root and reject
		// a type next to anyOf. Preserve the properties/required validation and
		// drop only the impossible root union constraint.
		delete(node, "anyOf")
		node["type"] = "object"
		if _, ok := node["properties"]; !ok {
			node["properties"] = map[string]any{}
		}
	} else if branches, ok := node["anyOf"].([]any); ok && len(branches) > 0 {
		// A parent type constrains every branch in regular JSON Schema. Copy it
		// into untyped branches before removing it from the parent, which is the
		// equivalent portable form. A branch that already declares
		// a type keeps its more specific declaration.
		parentType, hasParentType := node["type"]
		delete(node, "type")
		for _, rawBranch := range branches {
			branch, ok := rawBranch.(map[string]any)
			if !ok {
				continue
			}
			if _, hasType := branch["type"]; !hasType {
				switch {
				case hasParentType:
					branch["type"] = parentType
				case schemaDescribesObject(branch):
					branch["type"] = "object"
				}
			}
		}
	} else if _, hasType := node["type"]; !hasType && schemaDescribesObject(node) {
		node["type"] = "object"
	}

	for _, keyword := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
		if schemas, ok := node[keyword].(map[string]any); ok {
			for _, schema := range schemas {
				rewritePortableToolSchema(schema, false)
			}
		}
	}
	if items, exists := node["items"]; exists {
		switch schemas := items.(type) {
		case map[string]any:
			rewritePortableToolSchema(schemas, false)
		case []any:
			for _, schema := range schemas {
				rewritePortableToolSchema(schema, false)
			}
		}
	}
	for _, keyword := range []string{"additionalProperties", "unevaluatedProperties", "additionalItems", "unevaluatedItems", "not", "if", "then", "else", "contains", "propertyNames", "contentSchema"} {
		if schema, ok := node[keyword].(map[string]any); ok {
			rewritePortableToolSchema(schema, false)
		}
	}
	for _, keyword := range []string{"anyOf", "oneOf", "allOf", "prefixItems"} {
		if schemas, ok := node[keyword].([]any); ok {
			for _, schema := range schemas {
				rewritePortableToolSchema(schema, false)
			}
		}
	}
}

func schemaHasKnownKeyword(node map[string]any) bool {
	for _, keyword := range []string{
		"$schema", "$id", "$ref", "$defs", "definitions",
		"patternProperties", "dependentSchemas", "unevaluatedProperties",
		"anyOf", "oneOf", "allOf", "not", "if", "then", "else",
	} {
		if _, ok := node[keyword]; ok {
			return true
		}
	}
	return false
}

func schemaDescribesObject(node map[string]any) bool {
	for _, keyword := range []string{"properties", "required", "additionalProperties", "patternProperties", "dependentRequired", "dependentSchemas", "propertyNames", "minProperties", "maxProperties"} {
		if _, ok := node[keyword]; ok {
			return true
		}
	}
	return false
}

func mustMarshalSchema(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return emptyObjectSchema
	}
	return json.RawMessage(b)
}
