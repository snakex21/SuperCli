package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/tools/core"
)

// A real GunMayhem call requested more context than the schema allowed. This
// display preference should return bounded evidence, not cost a correction turn.
func TestSearchContextOversizedRequestIsBounded(t *testing.T) {
	dir := t.TempDir()
	writeSearchFixture(t, dir, "code.go", strings.Repeat("before\n", 41)+"needle\n"+strings.Repeat("after\n", 41))
	reg := core.NewRegistry()
	reg.MustRegister(NewSearchCode(dir).Spec())
	run := func(radius int) core.Result {
		t.Helper()
		result, err := reg.Execute(context.Background(), "search_code", json.RawMessage(fmt.Sprintf("{\"query\":\"needle\",\"context\":%d}", radius)))
		if err != nil || result.Err != nil {
			t.Fatalf("context=%d: %v %v", radius, err, result.Err)
		}
		return result
	}
	baseline := run(20)
	if strings.Count(baseline.Text, " | ") != 41 || !strings.Contains(baseline.Text, ">   42 | needle") {
		t.Fatalf("unexpected fixture output: %s", baseline.Text)
	}
	for _, radius := range []int{21, 80, 1000000, int(^uint(0) >> 1)} {
		got := run(radius)
		if !strings.HasPrefix(got.Text, baseline.Text) || strings.Count(got.Text, " | ") != 41 ||
			!strings.Contains(got.Text, "context limited to 20") {
			t.Fatalf("context=%d changed evidence or exceeded the cap: %s", radius, got.Text)
		}
	}
	result, err := reg.Execute(context.Background(), "search_code", json.RawMessage("{\"query\":\"needle\",\"context\":-1}"))
	if err == nil && result.Err == nil {
		t.Fatal("negative context must remain invalid")
	}
}
