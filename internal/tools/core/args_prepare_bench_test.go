package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func BenchmarkRegistryArgumentPreparation(b *testing.B) {
	cases := []struct {
		name, schema string
		args         any
	}{
		{"create_file_32k", `{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`, map[string]any{"path": "source.go", "content": strings.Repeat("func sample() { return }\n", 1400)}},
		{"memory_note_4k", `{"type":"object","properties":{"text":{"type":"string"},"scope":{"type":"string","enum":["project","global"]},"type":{"type":"string","enum":["fact","decision","task-log","preference"]}},"required":["text"]}`, map[string]any{"text": strings.Repeat("saved project context. ", 180), "scope": "project", "type": "decision"}},
		{"typed_read", `{"file":{"type":"string"},"from":{"type":"integer"},"to":{"type":"integer"}}`, map[string]any{"file": "source.go", "from": 1, "to": 100}},
		{"stringified_read", `{"file":{"type":"string"},"from":{"type":"integer"},"to":{"type":"integer"}}`, map[string]any{"file": "source.go", "from": "1", "to": "100"}},
		{"typed_argv", `{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"}},"timeout_ms":{"type":"integer"}},"required":["command"]}`, map[string]any{"command": []string{"git", "status", "--short"}, "timeout_ms": 5000}},
		{"stringified_argv", `{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"}},"timeout_ms":{"type":"integer"}},"required":["command"]}`, map[string]any{"command": `["git","status","--short"]`, "timeout_ms": "5000"}},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			reg := NewRegistry()
			reg.MustRegister(Tool{Name: "fixture", Description: "argument preparation fixture", Schema: tc.schema, Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{Text: "ok"}, nil }})
			raw, err := json.Marshal(tc.args)
			if err != nil {
				b.Fatal(err)
			}
			ctx := context.Background()
			result, err := reg.Execute(ctx, "fixture", raw)
			if err != nil || result.Err != nil {
				b.Fatalf("fixture rejected: %v / %v", err, result.Err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				result, err := reg.Execute(ctx, "fixture", raw)
				if err != nil || result.Err != nil {
					b.Fatalf("execution failed: %v / %v", err, result.Err)
				}
			}
		})
	}
}
