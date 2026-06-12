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

func decodeSchema(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v; raw=%s", err, raw)
	}
	return schema
}
