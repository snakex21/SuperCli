package search

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestToolSearcher_CompactSchemaPreservesContractAndDispatch(t *testing.T) {
	const schema = ` {
		"type": "object",
		"properties": {
			"mode": {"type": "string", "enum": ["safe  mode", "drugi\ttryb"], "description": "keep  two spaces\nand tab\tquoted \"text\""},
			"count": {"type": "integer", "minimum": 1, "maximum": 3}
		},
		"required": ["mode", "count"],
		"additionalProperties": false,
		"examples": [9007199254740993, 1e+09, -0]
	} `
	reg := NewRegistry()
	calls := 0
	reg.MustRegister(Tool{
		Name: "contract_tool", Description: "Schema contract fixture", Schema: schema, ReadOnly: true,
		Fn: func(context.Context, json.RawMessage) (Result, error) {
			calls++
			return Result{Text: "executed"}, nil
		},
	})
	result, err := NewToolSearcher(reg, nil).execute(context.Background(), json.RawMessage(`{"query":"contract_tool"}`))
	if err != nil || result.Err != nil {
		t.Fatalf("discovery failed: err=%v result=%v", err, result.Err)
	}
	var response struct {
		Matches []struct {
			Name      string `json:"name"`
			Schema    string `json:"schema"`
			Signature string `json:"signature"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(result.Text), &response); err != nil {
		t.Fatalf("discovery result: %v", err)
	}
	if len(response.Matches) != 1 || response.Matches[0].Name != "contract_tool" {
		t.Fatalf("unexpected matches: %+v", response.Matches)
	}
	match := response.Matches[0]
	if len(match.Schema) >= len(schema) {
		t.Fatalf("discovery schema did not shrink: %d >= %d", len(match.Schema), len(schema))
	}
	var before, after any
	for _, target := range []struct {
		text  string
		value *any
	}{{schema, &before}, {match.Schema, &after}} {
		decoder := json.NewDecoder(strings.NewReader(target.text))
		decoder.UseNumber()
		if err := decoder.Decode(target.value); err != nil {
			t.Fatalf("schema decode: %v", err)
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("discovery changed the schema contract: before=%v after=%v", before, after)
	}
	if !strings.Contains(match.Schema, `[9007199254740993,1e+09,-0]`) {
		t.Fatalf("numeric spellings changed: %s", match.Schema)
	}
	registered, _ := reg.Get("contract_tool")
	if registered.Schema != schema || match.Signature != toolSignature(registered.Name, schema) {
		t.Fatal("discovery changed the registered schema or signature")
	}
	if !reg.IsActive("contract_tool") {
		t.Fatal("discovered tool was not activated")
	}
	for _, tc := range []struct {
		args  string
		valid bool
	}{
		{`{"mode":"safe  mode","count":2}`, true},
		{`{"mode":"safe mode","count":2}`, false},
		{`{"mode":"safe  mode","count":0}`, false},
		{`{"mode":"safe  mode","count":4}`, false},
		{`{"mode":"safe  mode","count":1.5}`, false},
		{`{"mode":"safe  mode","count":2,"extra":true}`, false},
		{`{"mode":"safe  mode"}`, false},
	} {
		result, err := reg.Execute(context.Background(), "contract_tool", json.RawMessage(tc.args))
		if (err == nil && result.Err == nil) != tc.valid {
			t.Errorf("dispatch for %s: err=%v result=%v, valid=%v", tc.args, err, result.Err, tc.valid)
		}
	}
	if calls != 1 {
		t.Fatalf("executed %d calls, want only the valid call", calls)
	}
}

func TestToolSearcher_CompactSchemaKeepsEmptyAndMatchOrder(t *testing.T) {
	schemas := map[string]string{
		"empty_tool":   "",
		"compact_tool": `{"type":"object","description":"keep  spaces and \\ escapes"}`,
	}
	reg := NewRegistry()
	for _, name := range []string{"empty_tool", "compact_tool"} {
		reg.MustRegister(Tool{
			Name: name, Description: "Schema fallback fixture", Schema: schemas[name],
			Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{}, nil },
		})
	}
	order := []string{"compact_tool", "empty_tool"}
	args, _ := json.Marshal(map[string]any{"query": strings.Join(order, " "), "limit": len(order)})
	result, err := NewToolSearcher(reg, nil).execute(context.Background(), args)
	if err != nil || result.Err != nil {
		t.Fatalf("discovery failed: err=%v result=%v", err, result.Err)
	}
	var response struct {
		Matches []struct {
			Name   string `json:"name"`
			Schema string `json:"schema"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(result.Text), &response); err != nil {
		t.Fatalf("discovery result: %v", err)
	}
	if len(response.Matches) != len(order) {
		t.Fatalf("returned %d matches, want %d", len(response.Matches), len(order))
	}
	for i, match := range response.Matches {
		if match.Name != order[i] || match.Schema != schemas[match.Name] {
			t.Errorf("match %d changed order or fallback: %+v", i, match)
		}
		registered, _ := reg.Get(match.Name)
		if registered.Schema != schemas[match.Name] {
			t.Errorf("registered schema for %s changed", match.Name)
		}
	}
}

func TestCompactDiscoverySchemaPreservesInvalidText(t *testing.T) {
	for _, schema := range []string{"", " \t\r\n", "{\n \"type\": \"object\", \"properties\": } \n", "{} trailing"} {
		if got := compactDiscoverySchema(schema); got != schema {
			t.Errorf("invalid schema changed: got %q, want %q", got, schema)
		}
	}
}
