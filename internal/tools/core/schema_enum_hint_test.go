package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEnumErrorNamesAllowedValuesWithoutChangingValidation(t *testing.T) {
	p := newNamedValidationProbe(t, "remember", `{"type":"object","properties":{"type":{"type":"string","enum":["fact","decision","task-log","preference"]}}}`)
	result := p.requireInvalid(t, `{"type":"project"}`, `$.type: value is not in enum; allowed values: ["fact","decision","task-log","preference"]`)
	if strings.Contains(result.Err.Error(), "project") {
		t.Fatal("error unnecessarily echoed the invalid value")
	}
	p.requireValid(t, `{"type":"decision"}`)
	p.requireInvalid(t, `{"type":"Decision"}`, "value is not in enum")
}

func TestEnumHintsKeepJSONTypesAndEscaping(t *testing.T) {
	p := newValidationProbe(t, `{"type":"object","properties":{"items":{"type":"array","items":{"enum":[null,true,1.5,"x\nż"]}}}}`)
	p.requireInvalid(t, `{"items":[false]}`, `$.items[0]: value is not in enum; allowed values: [null,true,1.5,"x\nż"]`)
	p.requireValid(t, `{"items":[null,true,1.50,"x\nż"]}`)
	p.requireInvalid(t, `{"items":["1.5"]}`, "value is not in enum")
}

func TestEnumErrorDoesNotExpandLargeOrStructuredChoices(t *testing.T) {
	many := make([]any, 17)
	for i := range many {
		many[i] = i
	}
	for _, values := range [][]any{
		many,
		{strings.Repeat("large", 20000)},
		{map[string]any{"text": strings.Repeat("nested", 20000)}},
		{[]any{1, 2, 3}},
		{strings.Repeat("a", 120), strings.Repeat("b", 120), strings.Repeat("c", 120), strings.Repeat("d", 120), strings.Repeat("e", 120)},
	} {
		schema, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{"choice": map[string]any{"enum": values}}})
		p := newValidationProbe(t, string(schema))
		result := p.requireInvalid(t, `{"choice":"invalid"}`, "value is not in enum")
		if strings.Contains(result.Err.Error(), "allowed values:") || len(result.Err.Error()) > 150 {
			t.Fatal("unbounded or partial choices leaked into the error")
		}
		accepted, _ := json.Marshal(map[string]any{"choice": values[0]})
		p.requireValid(t, string(accepted))
	}
}
