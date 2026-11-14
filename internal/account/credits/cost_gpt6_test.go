package credits

import (
	"math"
	"testing"
)

// Prices from developers.openai.com/docs/models/<id> (checked 2026-11-14).
func TestRateForGPT6Family(t *testing.T) {
	for _, c := range []struct {
		model               string
		input, cached, outp float64 // USD per 1M
	}{
		{"gpt-6-luna", 0.10, 0.01, 0.50},
		{"gpt-6-sol", 2.00, 0.20, 10.00},
		{"gpt-6.1-sol", 2.00, 0.10, 10.00},
		{"gpt-6-astra", 10.00, 1.00, 50.00},
		{"openai/gpt-6-luna", 0.10, 0.01, 0.50},
	} {
		got, key := RateFor(c.model)
		if key == "default" {
			t.Fatalf("%s has no catalog price", c.model)
		}
		if !closeRate(got.InputPer1k*1000, c.input) || !closeRate(got.CachedInputPer1k*1000, c.cached) || !closeRate(got.OutputPer1k*1000, c.outp) {
			t.Errorf("%s = %+v per 1k", c.model, got)
		}
	}
	// 1M fresh-equivalent: 600k fresh ($0.06) + 400k cached ($0.004) + 100k out ($0.05).
	cost, known := CostAtRate(modelRates["gpt-6-luna"], 1_000_000, 100_000, 400_000)
	if !known || math.Abs(cost-0.114) > 1e-9 {
		t.Fatalf("luna cost = %v known=%v, want 0.114", cost, known)
	}
}
