package search

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSearchAutomaticContextProvidesAnswerWithoutAnotherRead(t *testing.T) {
	for _, useRG := range []bool{false, true} {
		t.Run(map[bool]string{false: "fallback", true: "ripgrep"}[useRG], func(t *testing.T) {
			if !useRG {
				t.Setenv("PATH", t.TempDir())
			} else if !NewSearchCode(".").hasRG() {
				t.Skip("rg unavailable")
			}
			dir := t.TempDir()
			writeSearchFixture(t, dir, "src/retry.go", "package retry\nfunc RetryDelay() int {\n return 235\n}\n")
			got, err := NewSearchCode(dir).run(context.Background(), json.RawMessage("{\"query\":\"func RetryDelay\"}"))
			if err != nil || got.Err != nil || !strings.Contains(got.Text, "return 235") || !strings.Contains(got.Text, "src/retry.go:1-4") {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

func TestSearchAutomaticContextBoundsExtraText(t *testing.T) {
	for _, test := range []struct{ name, body, args string }{
		{"broad", strings.Repeat("needle\n", 4), "{\"query\":\"needle\"}"},
		{"limited", "needle\nbody\n", "{\"query\":\"needle\",\"max\":1}"},
		{"large", "needle\n" + strings.Repeat("x", 4000) + "\n", "{\"query\":\"needle\"}"},
		{"explicit zero", "needle\nbody\n", "{\"query\":\"needle\",\"context\":0}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSearchFixture(t, dir, "a.txt", test.body)
			got, err := NewSearchCode(dir).run(context.Background(), json.RawMessage(test.args))
			if err != nil || got.Err != nil || strings.Contains(got.Text, "== a.txt:") || !strings.Contains(got.Text, "a.txt:1:needle") {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}
