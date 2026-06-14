package tools

import (
	"encoding/json"
	"testing"
)

// toolSearchSchema mirrors the real tool_search schema closely enough
// to exercise the {"limit":"5"} stringified-int case that fail-looped
// small local models.
const toolSearchSchema = `{
	"type": "object",
	"properties": {
		"query": {"type": "string"},
		"limit": {"type": "integer", "default": 3}
	},
	"required": ["query"]
}`

func TestCoerceArgs_StringifiedInt(t *testing.T) {
	// The headline bug: model sends {"limit":"5"} (string) for an int
	// field. After coercion it must unmarshal cleanly into searchArgs.
	in := json.RawMessage(`{"query":"find files","limit":"5"}`)
	out := CoerceArgs(toolSearchSchema, in)

	var a searchArgs
	if err := json.Unmarshal(out, &a); err != nil {
		t.Fatalf("coerced args still fail to unmarshal: %v (out=%s)", err, out)
	}
	if a.Limit != 5 {
		t.Errorf("limit = %d, want 5 (out=%s)", a.Limit, out)
	}
	if a.Query != "find files" {
		t.Errorf("query = %q, want %q", a.Query, "find files")
	}
}

func TestCoerceArgs_StringifiedBoolAndFloat(t *testing.T) {
	schema := `{"type":"object","properties":{
		"flag":{"type":"boolean"},
		"ratio":{"type":"number"}
	}}`
	in := json.RawMessage(`{"flag":"true","ratio":"1.50"}`)
	out := CoerceArgs(schema, in)

	var got struct {
		Flag  bool    `json:"flag"`
		Ratio float64 `json:"ratio"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal failed: %v (out=%s)", err, out)
	}
	if !got.Flag {
		t.Errorf("flag = %v, want true", got.Flag)
	}
	if got.Ratio != 1.5 {
		t.Errorf("ratio = %v, want 1.5", got.Ratio)
	}
}

func TestCoerceArgs_AlreadyCorrectTypesUntouched(t *testing.T) {
	in := json.RawMessage(`{"query":"x","limit":7}`)
	out := CoerceArgs(toolSearchSchema, in)
	var a searchArgs
	if err := json.Unmarshal(out, &a); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if a.Limit != 7 {
		t.Errorf("limit = %d, want 7", a.Limit)
	}
}

func TestCoerceArgs_NonNumericIntLeftForRealError(t *testing.T) {
	// A genuinely-wrong value ("abc" for an int) must NOT be coerced —
	// the tool should still surface the real error rather than us
	// silently guessing.
	in := json.RawMessage(`{"query":"x","limit":"abc"}`)
	out := CoerceArgs(toolSearchSchema, in)
	var a searchArgs
	if err := json.Unmarshal(out, &a); err == nil {
		t.Errorf("expected unmarshal to still fail for limit=\"abc\", got %+v (out=%s)", a, out)
	}
}

func TestCoerceArgs_NoSchemaReturnsInput(t *testing.T) {
	in := json.RawMessage(`{"limit":"5"}`)
	if out := CoerceArgs("", in); string(out) != string(in) {
		t.Errorf("no schema should be a no-op; got %s", out)
	}
}

func TestCoerceArgs_NonObjectArgsReturnedUnchanged(t *testing.T) {
	in := json.RawMessage(`[1,2,3]`)
	if out := CoerceArgs(toolSearchSchema, in); string(out) != string(in) {
		t.Errorf("array args should pass through unchanged; got %s", out)
	}
}

func TestCoerceArgs_UnknownFieldsPreserved(t *testing.T) {
	// Fields not in the schema must be preserved verbatim (we only
	// rewrite what we recognise).
	in := json.RawMessage(`{"query":"x","limit":"5","extra":"keep"}`)
	out := CoerceArgs(toolSearchSchema, in)
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if string(got["extra"]) != `"keep"` {
		t.Errorf("extra field lost/altered: %s", got["extra"])
	}
}
