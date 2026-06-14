package credits

import "testing"

func TestRateFor_Known(t *testing.T) {
	cases := []struct {
		model   string
		wantIn  float64
		wantOut float64
	}{
		{"gpt-4o-mini", 0.15, 0.60},
		{"gpt-4o", 2.50, 10.00},
		{"claude-sonnet-4-5", 3.00, 15.00},
		{"claude-haiku-4-5", 1.00, 5.00},
		{"llama-3.1-70b-versatile", 0.59, 0.79},
		{"gemini-2.0-flash", 0.10, 0.40},
		{"deepseek-reasoner", 0.55, 2.19},
		{"o1", 15.00, 60.00},
	}
	for _, c := range cases {
		got, key := RateFor(c.model)
		if key == "default" {
			t.Errorf("RateFor(%q) returned default", c.model)
		}
		if got.InputPer1k != c.wantIn {
			t.Errorf("RateFor(%q).Input = %v, want %v", c.model, got.InputPer1k, c.wantIn)
		}
		if got.OutputPer1k != c.wantOut {
			t.Errorf("RateFor(%q).Output = %v, want %v", c.model, got.OutputPer1k, c.wantOut)
		}
	}
}

func TestRateFor_Aliases(t *testing.T) {
	// Provider prefix
	r, _ := RateFor("openai/gpt-4o")
	if r.InputPer1k != 2.50 {
		t.Errorf("openai/gpt-4o -> %v, want 2.50", r.InputPer1k)
	}
	// Date suffix
	r, _ = RateFor("gpt-4o-2024-08-06")
	if r.InputPer1k != 2.50 {
		t.Errorf("gpt-4o-2024-08-06 -> %v, want 2.50", r.InputPer1k)
	}
	// Case insensitive
	r, _ = RateFor("GPT-4O")
	if r.InputPer1k != 2.50 {
		t.Errorf("GPT-4O -> %v, want 2.50", r.InputPer1k)
	}
}

func TestRateFor_Unknown_ReturnsDefault(t *testing.T) {
	r, key := RateFor("nonexistent-model")
	if key != "default" {
		t.Errorf("expected default key, got %q", key)
	}
	if r.InputPer1k != 1.00 || r.OutputPer1k != 3.00 {
		t.Errorf("default rate wrong: %+v", r)
	}
}

func TestRateFor_Empty_ReturnsDefault(t *testing.T) {
	r, key := RateFor("")
	if key != "default" {
		t.Errorf("expected default, got %q", key)
	}
	if r.InputPer1k != 1.00 {
		t.Errorf("default InputPer1k = %v, want 1.00", r.InputPer1k)
	}
}

func TestCostFor_Basic(t *testing.T) {
	// gpt-4o-mini: 0.15 in, 0.60 out per 1k
	// 1000 in + 1000 out = 0.15 + 0.60 = 0.75
	got := CostFor("gpt-4o-mini", 1000, 1000)
	if got != 0.75 {
		t.Errorf("CostFor(1k in, 1k out) = %v, want 0.75", got)
	}
	// 100 in + 50 out = 0.015 + 0.030 = 0.045
	got = CostFor("gpt-4o-mini", 100, 50)
	if got != 0.045 {
		t.Errorf("CostFor(100 in, 50 out) = %v, want 0.045", got)
	}
}

func TestCostFor_Zero(t *testing.T) {
	if got := CostFor("gpt-4o", 0, 0); got != 0 {
		t.Errorf("zero tokens should be 0, got %v", got)
	}
	// gpt-4o output: 10.00/1k. 1000 tokens = $10.
	if got := CostFor("gpt-4o", 0, 1000); got != 10.00 {
		t.Errorf("output-only 1k gpt-4o = %v, want 10.00", got)
	}
	if got := CostFor("gpt-4o", 1000, 0); got != 2.50 {
		t.Errorf("input-only 1k gpt-4o = %v, want 2.50", got)
	}
}

func TestCostFor_UsesDefaultForUnknown(t *testing.T) {
	// default: 1.00 in, 3.00 out
	// 1000 in + 1000 out = 1.00 + 3.00 = 4.00
	got := CostFor("garbage-model", 1000, 1000)
	if got != 4.00 {
		t.Errorf("CostFor(garbage, 1k, 1k) = %v, want 4.00", got)
	}
}

func TestFormatUSD(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "$0.00"},
		{0.0001, "$0.00"},
		{0.0123, "$0.01"},
		{0.75, "$0.75"},
		{1.5, "$1.50"},
		{99.99, "$99.99"},
		{1234.5678, "$1235"},
		{-2.50, "-$2.50"},
	}
	for _, c := range cases {
		got := FormatUSD(c.in)
		if got != c.want {
			t.Errorf("FormatUSD(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// F28: SetFetchedRates overrides hardcoded rates.
func TestSetFetchedRates_OverridesHardcoded(t *testing.T) {
	defer SetFetchedRates(nil) // cleanup

	// Set a fetched rate for gpt-4o at half the hardcoded price.
	SetFetchedRates(map[string]Rate{
		"gpt-4o": {InputPer1k: 1.25, OutputPer1k: 5.00},
	})
	r, key := RateFor("gpt-4o")
	if r.InputPer1k != 1.25 {
		t.Errorf("fetched gpt-4o input = %v, want 1.25", r.InputPer1k)
	}
	if r.OutputPer1k != 5.00 {
		t.Errorf("fetched gpt-4o output = %v, want 5.00", r.OutputPer1k)
	}
	if key != "gpt-4o (fetched)" {
		t.Errorf("key = %q, want 'gpt-4o (fetched)'", key)
	}
	// CostFor should use the fetched rate.
	cost := CostFor("gpt-4o", 1000, 1000)
	want := 1.25 + 5.00 // 6.25
	if cost != want {
		t.Errorf("CostFor with fetched rate = %v, want %v", cost, want)
	}
}

// F28: Unknown model with fetched rates still falls back to default.
func TestSetFetchedRates_UnknownStillDefault(t *testing.T) {
	defer SetFetchedRates(nil) // cleanup

	SetFetchedRates(map[string]Rate{
		"gpt-4o": {InputPer1k: 1.25, OutputPer1k: 5.00},
	})
	r, key := RateFor("unknown-model")
	if key != "default" {
		t.Errorf("expected default, got %q", key)
	}
	if r.InputPer1k != 1.00 {
		t.Errorf("default rate wrong: %v", r.InputPer1k)
	}
}

// F28: SetFetchedRates(nil) restores hardcoded rates.
func TestSetFetchedRates_NilRestoresHardcoded(t *testing.T) {
	SetFetchedRates(map[string]Rate{
		"gpt-4o": {InputPer1k: 0.01, OutputPer1k: 0.01},
	})
	SetFetchedRates(nil)
	r, _ := RateFor("gpt-4o")
	if r.InputPer1k != 2.50 {
		t.Errorf("after nil, gpt-4o input = %v, want 2.50 (hardcoded)", r.InputPer1k)
	}
}

// F28: GetFetchedRates returns a copy.
func TestGetFetchedRates_ReturnsCopy(t *testing.T) {
	defer SetFetchedRates(nil) // cleanup

	SetFetchedRates(map[string]Rate{
		"gpt-4o": {InputPer1k: 1.25},
	})
	got := GetFetchedRates()
	entry := got["gpt-4o"]
	entry.InputPer1k = 999
	got["gpt-4o"] = entry // write back (but should not matter)
	r, _ := RateFor("gpt-4o")
	if r.InputPer1k != 1.25 {
		t.Errorf("mutation leaked into fetched rates: %v", r.InputPer1k)
	}
}

// Per-endpoint override wins over fetched and hardcoded rates.
func TestRateForProvider_EndpointOverride(t *testing.T) {
	defer SetProviderRates(nil)
	defer SetFetchedRates(nil)

	// Hardcoded gpt-4o = 2.50/10.00; fetched = 1.25/5.00.
	SetFetchedRates(map[string]Rate{
		"gpt-4o": {InputPer1k: 1.25, OutputPer1k: 5.00},
	})
	// Proxy "myproxy" prices gpt-4o cheaper still. Mixed-case key
	// must still match a lowercased lookup.
	SetProviderRates(map[string]Rate{
		"MyProxy/gpt-4o": {InputPer1k: 0.50, OutputPer1k: 1.00},
	})

	r, key := RateForProvider("myproxy", "gpt-4o")
	if r.InputPer1k != 0.50 || r.OutputPer1k != 1.00 {
		t.Errorf("endpoint override not applied: %+v", r)
	}
	if key != "myproxy/gpt-4o (endpoint)" {
		t.Errorf("key = %q, want 'myproxy/gpt-4o (endpoint)'", key)
	}

	// Cost uses the override.
	if got := CostForProvider("myproxy", "gpt-4o", 1000, 1000); got != 1.50 {
		t.Errorf("CostForProvider with override = %v, want 1.50", got)
	}

	// A different provider falls through to the fetched rate.
	r, _ = RateForProvider("openai", "gpt-4o")
	if r.InputPer1k != 1.25 {
		t.Errorf("other provider should use fetched rate, got %v", r.InputPer1k)
	}

	// No provider behaves like plain RateFor (fetched rate).
	r, _ = RateFor("gpt-4o")
	if r.InputPer1k != 1.25 {
		t.Errorf("RateFor should still use fetched rate, got %v", r.InputPer1k)
	}
}

// Provider override for an unconfigured model falls through cleanly.
func TestRateForProvider_NoOverrideFallsThrough(t *testing.T) {
	defer SetProviderRates(nil)
	SetProviderRates(map[string]Rate{
		"myproxy/gpt-4o": {InputPer1k: 0.50, OutputPer1k: 1.00},
	})
	// Same proxy, different model -> hardcoded gpt-4o-mini rate.
	r, key := RateForProvider("myproxy", "gpt-4o-mini")
	if r.InputPer1k != 0.15 || key != "gpt-4o-mini" {
		t.Errorf("expected hardcoded gpt-4o-mini, got %+v key=%q", r, key)
	}
}

// F28: GetFetchedRates returns nil when no fetched rates.
func TestGetFetchedRates_NilWhenEmpty(t *testing.T) {
	defer SetFetchedRates(nil) // cleanup

	SetFetchedRates(nil)
	if got := GetFetchedRates(); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}
