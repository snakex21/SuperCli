package llm

import (
	"encoding/json"
	"testing"
)

func TestNormalizeToolSchemaWrapsHistoricalPropertiesOnlySchema(t *testing.T) {
	raw := `{"file":{"type":"string"},"from":{"type":"integer"},"to":{"type":"integer"}}`
	schema := decodeSchema(t, normalizeToolSchema(raw))

	if schema["type"] != "object" {
		t.Fatalf("type = %v, want object; schema=%v", schema["type"], schema)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties missing or wrong type: %v", schema["properties"])
	}
	for _, key := range []string{"file", "from", "to"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("property %q missing in %v", key, props)
		}
	}
}

func TestNormalizeToolSchemaAddsMissingObjectType(t *testing.T) {
	raw := `{"properties":{"query":{"type":"string"}},"required":["query"]}`
	schema := decodeSchema(t, normalizeToolSchema(raw))

	if schema["type"] != "object" {
		t.Fatalf("type = %v, want object; schema=%v", schema["type"], schema)
	}
	if _, ok := schema["properties"].(map[string]any)["query"]; !ok {
		t.Fatalf("query property missing: %v", schema)
	}
}

func TestNormalizeToolSchemaPreservesFullSchema(t *testing.T) {
	raw := `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`
	schema := decodeSchema(t, normalizeToolSchema(raw))

	if schema["type"] != "object" {
		t.Fatalf("type = %v, want object", schema["type"])
	}
	required := schema["required"].([]any)
	if len(required) != 1 || required[0] != "query" {
		t.Fatalf("required = %v, want [query]", required)
	}
}

func TestNormalizeToolSchemaHandlesEmptyOrInvalidSchema(t *testing.T) {
	for _, raw := range []string{"", `{}`, `{bad json`} {
		schema := decodeSchema(t, normalizeToolSchema(raw))
		if schema["type"] != "object" {
			t.Fatalf("%q: type = %v, want object", raw, schema["type"])
		}
		if _, ok := schema["properties"].(map[string]any); !ok {
			t.Fatalf("%q: properties missing: %v", raw, schema)
		}
	}
}

func TestNormalizeToolSchemaForMoonshotDropsRootAnyOf(t *testing.T) {
	raw := `{"type":"object","properties":{"question":{"type":"string"},"questions":{"type":"array"}},"anyOf":[{"required":["question"]},{"required":["questions"]}]}`
	schema := decodeSchema(t, normalizeToolSchemaForModel(raw, "zyloo/kimi-k3"))

	if schema["type"] != "object" {
		t.Fatalf("Moonshot tool root type = %v, want object: %v", schema["type"], schema)
	}
	if _, ok := schema["properties"].(map[string]any)["question"]; !ok {
		t.Fatalf("root properties were not preserved: %v", schema)
	}
	if _, exists := schema["anyOf"]; exists {
		t.Fatalf("root anyOf must be removed for Moonshot tool parameters: %v", schema)
	}
}

func TestNormalizeToolSchemaForMoonshotMovesNestedTypeIntoAnyOfBranches(t *testing.T) {
	raw := `{"type":"object","properties":{"choice":{"type":"object","anyOf":[{"required":["question"]},{"required":["questions"]}]}}}`
	schema := decodeSchema(t, normalizeToolSchemaForModel(raw, "moonshot/kimi-k3"))
	choice := schema["properties"].(map[string]any)["choice"].(map[string]any)
	if _, exists := choice["type"]; exists {
		t.Fatalf("nested anyOf parent type must be omitted: %v", choice)
	}
	branches := choice["anyOf"].([]any)
	for i, rawBranch := range branches {
		branch := rawBranch.(map[string]any)
		if branch["type"] != "object" {
			t.Fatalf("anyOf branch %d type = %v, want object", i, branch["type"])
		}
	}
}

func TestNormalizeToolSchemaForNonMoonshotKeepsConventionalRootType(t *testing.T) {
	raw := `{"type":"object","properties":{"question":{"type":"string"}},"anyOf":[{"required":["question"]}]}`
	schema := decodeSchema(t, normalizeToolSchemaForModel(raw, "gpt-5.6"))

	if schema["type"] != "object" {
		t.Fatalf("root type = %v, want object: %v", schema["type"], schema)
	}
	branch := schema["anyOf"].([]any)[0].(map[string]any)
	if _, exists := branch["type"]; exists {
		t.Fatalf("non-Moonshot branch type should not be rewritten: %v", branch)
	}
}

func decodeSchema(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v; raw=%s", err, raw)
	}
	return schema
}
