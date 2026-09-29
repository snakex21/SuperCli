package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestRegistryArgumentPreparationPreservesTypesAndValidation(t *testing.T) {
	stringsOnly := `{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`
	scalarTypes := `{"type":"object","properties":{"n":{"type":"integer"},"ratio":{"type":"number"},"enabled":{"type":"boolean"}}}`
	stringArray := `{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"}}},"required":["command"]}`
	cases := []struct {
		name, schema, input, want string
		rejected                  bool
	}{
		{"text stays exact", stringsOnly, ` { "text" : "42 true [1,2] <source>" } `, ` { "text" : "42 true [1,2] <source>" } `, false},
		{"typed scalars stay exact", scalarTypes, `{ "n" : 2, "ratio" : 1.50, "enabled" : true }`, `{ "n" : 2, "ratio" : 1.50, "enabled" : true }`, false},
		{"stringified scalars", scalarTypes, `{"n":"2","ratio":"1.50","enabled":"true"}`, `{"enabled":true,"n":2,"ratio":1.50}`, false},
		{"shorthand scalar", `{"n":{"type":"integer"}}`, `{"n":"7"}`, `{"n":7}`, false},
		{"stringified argv", stringArray, `{"command":"[\"git\",\"status\"]"}`, `{"command":["git","status"]}`, false},
		{"typed argv stays exact", stringArray, `{ "command" : ["git", "status"] }`, `{ "command" : ["git", "status"] }`, false},
		{"union retains string", `{"type":"object","properties":{"n":{"type":["integer","string"]}}}`, `{"n":"7"}`, `{"n":"7"}`, false},
		{"nullable number is not guessed", `{"type":"object","properties":{"n":{"type":["integer","null"]}}}`, `{"n":"7"}`, "", true},
		{"nested scalar is not guessed", `{"type":"object","properties":{"nested":{"type":"object","properties":{"n":{"type":"integer"}}}}}`, `{"nested":{"n":"7"}}`, "", true},
		{"array union items are not guessed", `{"type":"object","properties":{"command":{"type":"array","items":{"type":["string","integer"]}}}}`, `{"command":"[1,2]"}`, "", true},
		{"plain shell text is not guessed", stringArray, `{"command":"git status"}`, "", true},
		{"unknown text field still rejected", stringsOnly, `{"text":"x","unknown":true}`, "", true},
		{"null text still rejected", stringsOnly, `{"text":null}`, "", true},
		{"malformed text object still rejected", stringsOnly, `{"text":"x"`, "", true},
		{"non-finite number still rejected", scalarTypes, `{"n":"2","ratio":"NaN"}`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewRegistry()
			executions := 0
			reg.MustRegister(Tool{Name: "fixture", Description: "argument preservation fixture", Schema: tc.schema, Fn: func(_ context.Context, args json.RawMessage) (Result, error) {
				executions++
				return Result{Text: string(args)}, nil
			}})
			raw := json.RawMessage(tc.input)
			result, err := reg.Execute(context.Background(), "fixture", raw)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tc.input {
				t.Fatal("input arguments mutated")
			}
			if tc.rejected {
				if !errors.Is(result.Err, ErrInvalidToolArgs) || executions != 0 {
					t.Fatalf("rejected input reached implementation: result=%+v calls=%d", result, executions)
				}
				return
			}
			if result.Err != nil || executions != 1 || result.Text != tc.want {
				t.Fatalf("result=%+v calls=%d want raw=%q", result, executions, tc.want)
			}
		})
	}
}
