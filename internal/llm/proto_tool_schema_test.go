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

func TestCheckedToolSchemaRejectsInvalidJSON(t *testing.T) {
	for _, normalize := range []func(string) (json.RawMessage, error){normalizeToolSchemaChecked, normalizeOpenAIToolSchemaChecked} {
		if _, err := normalize(`{bad json`); err == nil {
			t.Fatal("checked schema normalization silently accepted invalid JSON")
		}
		if _, err := normalize(`null`); err == nil {
			t.Fatal("checked schema normalization silently accepted a non-object root")
		}
	}
}

func TestProviderRequestBuildersRejectInvalidToolSchema(t *testing.T) {
	tools := []ToolDef{{Name: "broken", Schema: `{bad json`}}
	msgs := []Message{{Role: RoleUser, Content: "hello"}}
	if _, err := buildOpenAIRequest("model", msgs, tools, true, false); err == nil {
		t.Fatal("OpenAI request builder accepted an invalid tool schema")
	}
	if _, err := buildAnthropicRequest("model", msgs, tools, true, 4096); err == nil {
		t.Fatal("Anthropic request builder accepted an invalid tool schema")
	}
	if _, err := buildCodexRequest("model", msgs, tools, true); err == nil {
		t.Fatal("Responses request builder accepted an invalid tool schema")
	}
}

func TestPortableSchemaKeepsFullLocalUnionAndTraversesDefinitions(t *testing.T) {
	raw := `{
		"type":"object",
		"anyOf":[{"required":["value"]},{"required":["fallback"]}],
		"properties":{"value":{"$ref":"#/$defs/item"}},
		"$defs":{"item":{"properties":{"name":{"type":"string"}}}},
		"definitions":{"legacy":{"properties":{"id":{"type":"integer"}}}},
		"patternProperties":{"^x":{"properties":{"flag":{"type":"boolean"}}}},
		"dependentSchemas":{"value":{"properties":{"fallback":{"type":"string"}}}},
		"unevaluatedProperties":{"properties":{"extra":{"type":"string"}}},
		"items":[{"properties":{"arrayValue":{"type":"string"}}}]
	}`
	fullRaw, err := normalizeToolSchemaChecked(raw)
	if err != nil {
		t.Fatal(err)
	}
	full := decodeSchema(t, fullRaw)
	if _, ok := full["anyOf"]; !ok {
		t.Fatal("full local schema lost its root anyOf")
	}

	portableRaw, err := normalizeOpenAIToolSchemaChecked(raw)
	if err != nil {
		t.Fatal(err)
	}
	portable := decodeSchema(t, portableRaw)
	if _, ok := portable["anyOf"]; ok {
		t.Fatal("portable wire schema retained incompatible root anyOf")
	}
	assertNestedObjectType := func(label string, value any) {
		t.Helper()
		if got := value.(map[string]any)["type"]; got != "object" {
			t.Fatalf("%s type = %v, want object", label, got)
		}
	}
	assertNestedObjectType("$defs", portable["$defs"].(map[string]any)["item"])
	assertNestedObjectType("definitions", portable["definitions"].(map[string]any)["legacy"])
	assertNestedObjectType("patternProperties", portable["patternProperties"].(map[string]any)["^x"])
	assertNestedObjectType("dependentSchemas", portable["dependentSchemas"].(map[string]any)["value"])
	assertNestedObjectType("unevaluatedProperties", portable["unevaluatedProperties"])
	assertNestedObjectType("array items", portable["items"].([]any)[0])
}

func TestNormalizeOpenAIToolSchemaDropsRootAnyOf(t *testing.T) {
	raw := `{"type":"object","properties":{"question":{"type":"string"},"questions":{"type":"array"}},"anyOf":[{"required":["question"]},{"required":["questions"]}]}`
	schema := decodeSchema(t, normalizeOpenAIToolSchema(raw))

	if schema["type"] != "object" {
		t.Fatalf("portable tool root type = %v, want object: %v", schema["type"], schema)
	}
	if _, ok := schema["properties"].(map[string]any)["question"]; !ok {
		t.Fatalf("root properties were not preserved: %v", schema)
	}
	if _, exists := schema["anyOf"]; exists {
		t.Fatalf("root anyOf must be removed from portable tool parameters: %v", schema)
	}
}

func TestNormalizeOpenAIToolSchemaMovesNestedTypeIntoAnyOfBranches(t *testing.T) {
	raw := `{"type":"object","properties":{"choice":{"type":"object","anyOf":[{"required":["question"]},{"required":["questions"]}]}}}`
	schema := decodeSchema(t, normalizeOpenAIToolSchema(raw))
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

func TestNormalizeOpenAIToolSchemaAddsTypesToNestedObjects(t *testing.T) {
	raw := `{
		"type":"object",
		"properties":{
			"config":{"properties":{"name":{"type":"string"}},"required":["name"]},
			"entries":{"type":"array","items":{"properties":{"value":{"type":"string"}}}},
			"choice":{"anyOf":[{"required":["left"]},{"properties":{"right":{"type":"string"}}}]}
		}
	}`
	schema := decodeSchema(t, normalizeOpenAIToolSchema(raw))
	properties := schema["properties"].(map[string]any)

	if _, bogusProperty := properties["type"]; bogusProperty {
		t.Fatalf("properties table was mistaken for a schema: %v", properties)
	}
	config := properties["config"].(map[string]any)
	if config["type"] != "object" {
		t.Fatalf("config type = %v, want object: %v", config["type"], config)
	}
	entries := properties["entries"].(map[string]any)
	items := entries["items"].(map[string]any)
	if items["type"] != "object" {
		t.Fatalf("array items type = %v, want object: %v", items["type"], items)
	}
	choice := properties["choice"].(map[string]any)
	if _, exists := choice["type"]; exists {
		t.Fatalf("untyped anyOf parent unexpectedly has a type: %v", choice)
	}
	for i, rawBranch := range choice["anyOf"].([]any) {
		branch := rawBranch.(map[string]any)
		if branch["type"] != "object" {
			t.Fatalf("choice branch %d type = %v, want object: %v", i, branch["type"], branch)
		}
	}
}

func TestNormalizeOpenAIToolSchemaRemovesAnyOfParentTypeWithMixedBranches(t *testing.T) {
	raw := `{"type":"object","properties":{"value":{"type":"string","anyOf":[{"maxLength":10},{"type":"null"}]}}}`
	schema := decodeSchema(t, normalizeOpenAIToolSchema(raw))
	value := schema["properties"].(map[string]any)["value"].(map[string]any)
	if _, exists := value["type"]; exists {
		t.Fatalf("anyOf parent type must be removed: %v", value)
	}
	branches := value["anyOf"].([]any)
	if got := branches[0].(map[string]any)["type"]; got != "string" {
		t.Fatalf("untyped branch inherited %v, want string", got)
	}
	if got := branches[1].(map[string]any)["type"]; got != "null" {
		t.Fatalf("explicit branch type changed to %v, want null", got)
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
