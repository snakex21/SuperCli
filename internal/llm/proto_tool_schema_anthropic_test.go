package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"supercli/internal/tools/core"
)

func anthropicSchemaObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func anthropicSchemaAccepts(t *testing.T, schema string, args string) bool {
	t.Helper()
	registry := core.NewRegistry()
	if err := registry.Register(core.Tool{Name: "fixture", Description: "fixture", Schema: schema, Fn: func(context.Context, json.RawMessage) (core.Result, error) { return core.Result{}, nil }}); err != nil {
		t.Fatal(err)
	}
	result, err := registry.Execute(context.Background(), "fixture", json.RawMessage(args))
	return err == nil && result.Err == nil
}

func TestAnthropicRootProjectionDoesNotNarrowAcceptedArguments(t *testing.T) {
	cases := []struct {
		name, raw string
		valid     []string
	}{
		{"anyOf_other_branch_permits_extra", `{"type":"object","additionalProperties":true,"anyOf":[{"properties":{"left":{"type":"string","minLength":2}},"required":["left"]},{"properties":{"right":{"type":"integer","minimum":1}},"required":["right"]}]}`, []string{`{"left":false,"right":2}`, `{"left":"ok","right":false}`, `{"left":"ok","right":2}`}},
		{"oneOf_other_branch_permits_extra", `{"type":"object","additionalProperties":true,"oneOf":[{"properties":{"left":{"type":"string"}},"required":["left"]},{"properties":{"right":{"type":"integer"}},"required":["right"]}]}`, []string{`{"left":false,"right":2}`, `{"left":"ok","right":false}`}},
		{"sealed_union", `{"type":"object","anyOf":[{"properties":{"left":{"type":"string"}},"required":["left"],"additionalProperties":false},{"properties":{"right":{"type":"integer"}},"required":["right"],"additionalProperties":false}]}`, []string{`{"left":"ok"}`, `{"right":2}`}},
		{"allOf_branch_fields", `{"type":"object","allOf":[{"properties":{"left":{"type":"string"}},"required":["left"]},{"properties":{"right":{"type":"integer"}},"required":["right"]}]}`, []string{`{"left":"ok","right":2}`}},
		{"allOf_nested_union", `{"type":"object","allOf":[{"anyOf":[{"properties":{"left":{"type":"string"}},"required":["left"]},{"properties":{"right":{"type":"integer"}},"required":["right"]}]},{"properties":{"common":{"type":"string"}},"required":["common"]}]}`, []string{`{"common":"ok","left":false,"right":2}`, `{"common":"ok","left":"ok","right":false}`}},
		{"root_additional_schema", `{"type":"object","additionalProperties":{"type":"integer"},"allOf":[{"properties":{"count":{"minimum":1}},"required":["count"]}]}`, []string{`{"count":2,"other":3}`}},
		{"boolean_union", `{"type":"object","additionalProperties":true,"anyOf":[true,{"properties":{"field":{"type":"string"}}}]}`, []string{`{"field":false}`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := normalizeAnthropicToolSchemaChecked(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			object := anthropicSchemaObject(t, encoded)
			// Registry normally seals tool roots. This test checks JSON Schema's wire
			// semantics, where an absent additionalProperties permits arbitrary values.
			if _, exists := object["additionalProperties"]; !exists {
				object["additionalProperties"] = true
			}
			wire, err := json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			for _, args := range tc.valid {
				if !anthropicSchemaAccepts(t, tc.raw, args) {
					t.Fatalf("invalid positive control: %s", args)
				}
				if !anthropicSchemaAccepts(t, string(wire), args) {
					t.Fatalf("projection newly rejected %s; wire=%s", args, wire)
				}
			}
		})
	}
}

func TestAnthropicRootProjectionPreservesNestedConstraintsAndRequired(t *testing.T) {
	raw := `{"type":"object","properties":{"mode":{"enum":["a","b"]},"data":{"type":"object","oneOf":[{"required":["x"]},{"required":["y"]}],"properties":{"x":{"type":"string"},"y":{"type":"string"}}}},"required":["mode"],"allOf":[{"properties":{"items":{"type":"array","minItems":1,"items":{"anyOf":[{"type":"integer","minimum":2},{"type":"string","minLength":3}]}}}}]}`
	encoded, err := normalizeAnthropicToolSchemaChecked(raw)
	if err != nil {
		t.Fatal(err)
	}
	original := anthropicSchemaObject(t, []byte(raw))
	wire := anthropicSchemaObject(t, encoded)
	if !reflect.DeepEqual(original["required"], wire["required"]) {
		t.Fatal("root required changed")
	}
	originalProps := original["properties"].(map[string]any)
	wireProps := wire["properties"].(map[string]any)
	for name, schema := range originalProps {
		if !reflect.DeepEqual(schema, wireProps[name]) {
			t.Fatalf("existing nested schema %q changed", name)
		}
	}
	branch := original["allOf"].([]any)[0].(map[string]any)
	if !reflect.DeepEqual(branch["properties"].(map[string]any)["items"], wireProps["items"]) {
		t.Fatal("branch-only nested constraints changed")
	}
	if anthropicSchemaAccepts(t, string(encoded), `{"mode":"a","items":[false]}`) {
		t.Fatal("preservable nested constraint disappeared")
	}
	if anthropicSchemaAccepts(t, string(encoded), `{"items":[2]}`) {
		t.Fatal("root required disappeared")
	}
}

func TestAnthropicRootProjectionLocalReferences(t *testing.T) {
	raw := `{"type":"object","$defs":{"supercli_root_branches":{},"branch":{"properties":{"from_ref":{"type":"array","items":{"type":"string"}}}}},"properties":{"same":{"$ref":"#/allOf/0/properties/value"},"literal":{"const":{"$ref":"#/allOf/0/properties/value"}}},"allOf":[{"properties":{"value":{"type":"string","minLength":2}}},{"$ref":"#/$defs/branch"}]}`
	encoded, err := normalizeAnthropicToolSchemaChecked(raw)
	if err != nil {
		t.Fatal(err)
	}
	wire := anthropicSchemaObject(t, encoded)
	props := wire["properties"].(map[string]any)
	if props["value"] == nil || props["from_ref"] == nil {
		t.Fatal("reference/envelope branch lost fields")
	}
	ref := props["same"].(map[string]any)["$ref"].(string)
	target := anthropicLocalSchemaRef(wire, ref)
	if !reflect.DeepEqual(target, map[string]any{"type": "string", "minLength": float64(2)}) {
		t.Fatalf("dangling or changed ref %q -> %#v", ref, target)
	}
	literal := props["literal"].(map[string]any)["const"].(map[string]any)["$ref"]
	if literal != "#/allOf/0/properties/value" {
		t.Fatal("instance const data rewritten as a schema")
	}
	defs := wire["$defs"].(map[string]any)
	if !reflect.DeepEqual(defs["supercli_root_branches"], map[string]any{}) {
		t.Fatal("existing definition overwritten")
	}
	if defs["supercli_root_branches_1"] == nil {
		t.Fatal("reference relocation missing")
	}
	cyclic := `{"type":"object","$defs":{"loop":{"$ref":"#/$defs/loop","properties":{"cycle_field":{"type":"string"}}}},"anyOf":[{"$ref":"#/$defs/loop"},{"properties":{"plain":{"type":"integer"}}}]}`
	result, err := normalizeAnthropicToolSchemaChecked(cyclic)
	if err != nil {
		t.Fatal(err)
	}
	fields := anthropicSchemaObject(t, result)["properties"].(map[string]any)
	if fields["cycle_field"] == nil || fields["plain"] == nil {
		t.Fatal("cyclic reference lost named fields")
	}
}

func TestAnthropicRootProjectionDropsEvaluationDependentClosure(t *testing.T) {
	raw := `{"type":"object","unevaluatedProperties":false,"allOf":[{"patternProperties":{"^x":{"type":"string"}},"properties":{"named":{"type":"integer"}}}]}`
	encoded, err := normalizeAnthropicToolSchemaChecked(raw)
	if err != nil {
		t.Fatal(err)
	}
	wire := anthropicSchemaObject(t, encoded)
	if _, exists := wire["unevaluatedProperties"]; exists {
		t.Fatal("removed branch evaluations would newly reject previously evaluated properties")
	}
	// No combinator means there is no need to relax an ordinary object schema.
	ordinary := `{"type":"object","properties":{},"unevaluatedProperties":false}`
	encoded, err = normalizeAnthropicToolSchemaChecked(ordinary)
	if err != nil {
		t.Fatal(err)
	}
	if anthropicSchemaObject(t, encoded)["unevaluatedProperties"] != false {
		t.Fatal("ordinary schema unnecessarily changed")
	}
}

func TestAnthropicToolSchemaModeCacheOwnership(t *testing.T) {
	resetSchemaResidencyCache()
	t.Cleanup(resetSchemaResidencyCache)
	raw := `{"type":"object","properties":{"base":{"type":"string"}},"oneOf":[{"properties":{"left":{"type":"string"}}}],"anyOf":[{"required":["base"]}]}`
	expected := make(map[toolSchemaMode]json.RawMessage)
	for _, mode := range []toolSchemaMode{toolSchemaFull, toolSchemaPortable, toolSchemaAnthropic} {
		encoded, err := cachedToolSchema(raw, mode)
		if err != nil {
			t.Fatal(err)
		}
		expected[mode] = append(json.RawMessage(nil), encoded...)
		clear(encoded)
		again, err := cachedToolSchema(raw, mode)
		if err != nil || !bytes.Equal(again, expected[mode]) {
			t.Fatalf("mode %d cached value shares caller ownership", mode)
		}
	}
	if anthropicSchemaObject(t, expected[toolSchemaFull])["oneOf"] == nil {
		t.Fatal("full schema weakened")
	}
	portable := anthropicSchemaObject(t, expected[toolSchemaPortable])
	if portable["oneOf"] == nil || portable["anyOf"] != nil {
		t.Fatal("existing Portable behavior changed")
	}
	projected := anthropicSchemaObject(t, expected[toolSchemaAnthropic])
	for _, keyword := range []string{"oneOf", "allOf", "anyOf"} {
		if projected[keyword] != nil {
			t.Fatalf("root %s retained", keyword)
		}
	}
	var wg sync.WaitGroup
	errors := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(mode toolSchemaMode) {
			defer wg.Done()
			encoded, err := cachedToolSchema(raw, mode)
			if err != nil || !bytes.Equal(encoded, expected[mode]) {
				errors <- fmt.Errorf("mode %d cache cross-contamination: %v", mode, err)
			}
			clear(encoded)
		}(toolSchemaMode(i % 3))
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	assertSchemaResidencyAccounting(t)
	normalizedToolSchemaCache.Lock()
	count := len(normalizedToolSchemaCache.entries)
	normalizedToolSchemaCache.Unlock()
	if count != 3 {
		t.Fatalf("mode-isolated entries=%d, want 3", count)
	}
}

func TestAnthropicRequestCachingBothModesUseProjectedSchema(t *testing.T) {
	raw := `{"type":"object","anyOf":[{"properties":{"branch":{"type":"string"}}}]}`
	for _, caching := range []bool{false, true} {
		body, err := buildAnthropicRequestWithCaching("claude-fixture", []Message{{Role: RoleUser, Content: "fixture"}}, []ToolDef{{Name: "fixture", Schema: raw}}, false, 64, Sampling{}, caching)
		if err != nil {
			t.Fatal(err)
		}
		var request anthropicRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		wire := anthropicSchemaObject(t, request.Tools[0].InputSchema)
		if wire["anyOf"] != nil || wire["properties"].(map[string]any)["branch"] == nil {
			t.Fatalf("caching=%t did not use projection", caching)
		}
	}
	for _, raw := range []string{`{"type":`, `[]`, `null`} {
		if _, err := normalizeAnthropicToolSchemaChecked(raw); err == nil {
			t.Fatalf("malformed schema accepted: %s", raw)
		}
	}
}
