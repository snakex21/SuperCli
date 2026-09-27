package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestToolArgumentRepairRemainsSchemaChecked(t *testing.T) {
	for _, tc := range []struct {
		name, input, candidate string
		repair                 bool
		wantCalls, wantRepairs int
	}{
		{name: "valid untouched", input: "{\"count\":1}", candidate: "invalid", repair: true, wantCalls: 1},
		{name: "ordinary coercion untouched", input: "{\"count\":\"1\"}", candidate: "invalid", repair: true, wantCalls: 1},
		{name: "valid repair", input: "{\"wrong\":1}", candidate: "{\"count\":1}", repair: true, wantCalls: 1, wantRepairs: 1},
		{name: "repair coercion", input: "{\"wrong\":1}", candidate: "{\"count\":\"1\"}", repair: true, wantCalls: 1, wantRepairs: 1},
		{name: "declined", input: "{\"wrong\":1}", candidate: "{\"count\":1}", wantRepairs: 1},
		{name: "still invalid", input: "{\"wrong\":1}", candidate: "{\"count\":0}", repair: true, wantRepairs: 1},
		{name: "malformed repair", input: "{\"wrong\":1}", candidate: "{", repair: true, wantRepairs: 1},
		{name: "another unknown field", input: "{\"wrong\":1}", candidate: "{\"count\":1,\"extra\":true}", repair: true, wantRepairs: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repairs, calls := 0, 0
			tool := Tool{Name: "fixture", Description: "fixture", Schema: "{\"type\":\"object\",\"properties\":{\"count\":{\"type\":\"integer\",\"minimum\":1}},\"required\":[\"count\"]}", RepairArgs: func(raw json.RawMessage) (json.RawMessage, bool) {
				repairs++
				if string(raw) != tc.input {
					t.Fatal("repair did not receive original input")
				}
				return json.RawMessage(tc.candidate), tc.repair
			}, Fn: func(_ context.Context, args json.RawMessage) (Result, error) {
				calls++
				return Result{Text: string(args)}, nil
			}}
			reg := NewRegistry()
			reg.MustRegister(tool)
			result, err := reg.Execute(context.Background(), tool.Name, json.RawMessage(tc.input))
			if err != nil || repairs != tc.wantRepairs || calls != tc.wantCalls {
				t.Fatalf("repairs=%d calls=%d err=%v", repairs, calls, err)
			}
			if calls == 0 && !errors.Is(result.Err, ErrInvalidToolArgs) {
				t.Fatalf("unvalidated repair accepted: %+v", result)
			}
			if calls == 1 && (result.Err != nil || !strings.Contains(result.Text, "\"count\":1")) {
				t.Fatalf("invalid execution: %+v", result)
			}
		})
	}
}
