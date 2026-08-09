package core

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCoerceCompiledArgs_JSONArrayString guards the lenient coercion
// of a JSON-encoded argv array passed as a string. Small/local models
// frequently send {"command": "[\"git\",\"status\"]"} instead of a real
// array; before this fix every such call died in schema validation
// ("expected array, got string"), costing a full retry step — visible
// repeatedly in run telemetry.
func TestCoerceCompiledArgs_JSONArrayString(t *testing.T) {
	schema, err := compileToolSchema(`{
		"type": "object",
		"properties": {
			"command": {"type": "array", "items": {"type": "string"}, "minItems": 1},
			"workdir": {"type": "string"}
		},
		"required": ["command"]
	}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	// A JSON-encoded array as a string is unwrapped to a real array.
	out := coerceCompiledArgs(schema, json.RawMessage(`{"command":"[\"git\",\"status\",\"--short\"]","workdir":"."}`))
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("coerced output is not an object: %v (%s)", err, out)
	}
	var command []string
	if err := json.Unmarshal(obj["command"], &command); err != nil {
		t.Fatalf("command not an array after coercion: %v (%s)", err, obj["command"])
	}
	if len(command) != 3 || command[0] != "git" || command[2] != "--short" {
		t.Errorf("command = %v", command)
	}
	// The real-array input must round-trip untouched.
	if err := schema.validateJSON(out); err != nil {
		t.Errorf("coerced args fail schema validation: %v", err)
	}

	// A plain shell string is NOT guessed at: it must stay a string and
	// still fail validation, so the model gets the exact schema error.
	out2 := coerceCompiledArgs(schema, json.RawMessage(`{"command":"git status --short"}`))
	var obj2 map[string]json.RawMessage
	if err := json.Unmarshal(out2, &obj2); err != nil {
		t.Fatalf("out2: %v", err)
	}
	var s string
	if err := json.Unmarshal(obj2["command"], &s); err != nil || s != "git status --short" {
		t.Errorf("plain string was altered: %v (%s)", s, out2)
	}
	if err := schema.validateJSON(out2); err == nil {
		t.Error("plain shell string should still fail validation")
	}

	// A real array input must be left exactly as it was.
	out3 := coerceCompiledArgs(schema, json.RawMessage(`{"command":["node","x.js"]}`))
	if !strings.Contains(string(out3), `"node"`) {
		t.Errorf("real array was altered: %s", out3)
	}
}

// TestCoerceCompiledArgs_NonArrayFieldsUntouched: scalar coercion
// still works and array coercion does not misfire on other types.
func TestCoerceCompiledArgs_NonArrayFieldsUntouched(t *testing.T) {
	schema, err := compileToolSchema(`{
		"type": "object",
		"properties": {
			"command": {"type": "array", "items": {"type": "string"}},
			"max": {"type": "integer"}
		}
	}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out := coerceCompiledArgs(schema, json.RawMessage(`{"command":"[\"ls\"]","max":"5"}`))
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("out: %v", err)
	}
	var command []string
	if err := json.Unmarshal(obj["command"], &command); err != nil || len(command) != 1 || command[0] != "ls" {
		t.Errorf("command = %v (%s)", command, obj["command"])
	}
	var max json.Number
	if err := json.Unmarshal(obj["max"], &max); err != nil || max.String() != "5" {
		t.Errorf("scalar coercion regressed: %v (%s)", max, obj["max"])
	}
}
