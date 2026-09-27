package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func BenchmarkSchemaValidationPayload(b *testing.B) {
	changes := make([]map[string]any, 20)
	for i := range changes {
		changes[i] = map[string]any{"old": strings.Repeat("old source line\n", 32), "new": strings.Repeat("new source line\n", 32), "expected_count": 1}
	}
	cases := []struct {
		name, schema string
		args         any
	}{
		{"small_search", `{"type":"object","properties":{"query":{"type":"string"},"max":{"type":"integer"}}}`, map[string]any{"query": "func Lookup", "max": 20}},
		{"memory_note", `{"type":"object","properties":{"text":{"type":"string"},"scope":{"type":"string","enum":["project","global"]},"type":{"type":"string","enum":["fact","decision","task-log","preference"]}}}`, map[string]any{"text": strings.Repeat("saved project context. ", 180), "scope": "project", "type": "decision"}},
		{"patch_20_changes", `{"type":"object","properties":{"path":{"type":"string"},"changes":{"type":"array","items":{"type":"object","properties":{"old":{"type":"string"},"new":{"type":"string"},"expected_count":{"type":"integer"}}}}}}`, map[string]any{"path": "source.go", "changes": changes}},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			schema, err := compileToolSchema(tc.schema)
			if err != nil {
				b.Fatal(err)
			}
			raw, _ := json.Marshal(tc.args)
			if err := schema.validateJSON(raw); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := schema.validateJSON(raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
