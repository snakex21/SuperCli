package stats

import (
	"strings"
	"testing"
)

func TestPrefillLineIncludesMeasuredFieldsWithoutPrompt(t *testing.T) {
	line := PrefillLine(Call{
		Purpose: "main", Provider: "connection", Model: "m",
		TTFTUs: 12_500_000, TokensIn: 10_000, TokensCached: 7_000,
		PrefillEvaluated: 3_000, PrefillTokensPerSecond: 240,
		PrefillBudget: 8_000, PrefillBudgetSource: "prefill-profile",
	})
	for _, want := range []string{"[prefill]", "evaluated=3000", "ttft_ms=12500", "tokens_per_second=240.0", "budget=8000"} {
		if !strings.Contains(line, want) {
			t.Fatalf("PrefillLine %q missing %q", line, want)
		}
	}
}
