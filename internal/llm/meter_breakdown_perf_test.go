package llm

import (
	"strings"
	"testing"
)

// Preserve the original estimator, including unusual whitespace from external
// tool descriptions/schemas. This is a CPU/allocation change, not recalibration.
func TestToolBreakdownPreservesPreviousEstimate(t *testing.T) {
	fields := []string{"", " ", "\t\r\n", "\v\f", "\u00a0", "\u2003abc\u00a0", "ą中😀", "arg name", `{"type":"object"}`}
	for _, name := range fields {
		for _, desc := range fields {
			for _, schema := range fields {
				text := strings.TrimSpace(name + " " + desc + " " + schema)
				want := 0
				if text != "" {
					want = EstimateMessageTokens(Message{Role: RoleSystem, Content: text})
				}
				got := EstimateRequestBreakdown(nil, []ToolDef{{Name: name, Description: desc, Schema: schema}})
				if got != (RequestBreakdown{Tool: want}) {
					t.Fatalf("%q/%q/%q: got=%+v want=%d", name, desc, schema, got, want)
				}
			}
		}
	}
}

func BenchmarkToolBreakdownLargeSchemas(b *testing.B) {
	defs := make([]ToolDef, 48)
	for i := range defs {
		defs[i] = ToolDef{Name: "extension_tool", Description: "Read structured project information", Schema: strings.Repeat(" schema field ", 500)}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EstimateRequestBreakdown(nil, defs)
	}
}
