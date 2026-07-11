package credits

import (
	"math"
	"testing"
)

func closeRate(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

func TestRateFor_Known(t *testing.T) {
	cases := []struct {
		model   string
		wantIn  float64
		wantOut float64
	}{
		{"gpt-4o-mini", 0.00015, 0.00060},
		{"gpt-4o", 0.00250, 0.01000},
		{"claude-sonnet-4-5", 0.00300, 0.01500},
		{"claude-haiku-4-5", 0.00100, 0.00500},
		{"llama-3.1-70b-versatile", 0.00059, 0.00079},
		{"gemini-2.0-flash", 0.00010, 0.00040},
		{"deepseek-reasoner", 0.00014, 0.00028},
		{"o1", 0.01500, 0.06000},
	}
	for _, c := range cases {
		got, key := RateFor(c.model)
		if key == "default" {
			t.Errorf("RateFor(%q) returned default", c.model)
		}
		if !closeRate(got.InputPer1k, c.wantIn) {
			t.Errorf("RateFor(%q).Input = %v, want %v", c.model, got.InputPer1k, c.wantIn)
		}
		if !closeRate(got.OutputPer1k, c.wantOut) {
			t.Errorf("RateFor(%q).Output = %v, want %v", c.model, got.OutputPer1k, c.wantOut)
		}
	}
}

func TestRateFor_Aliases(t *testing.T) {
	// Provider prefix
	r, _ := RateFor("openai/gpt-4o")
	if r.InputPer1k != 0.00250 {
		t.Errorf("openai/gpt-4o -> %v, want 0.00250", r.InputPer1k)
	}
	// Date suffix
	r, _ = RateFor("gpt-4o-2024-08-06")
	if r.InputPer1k != 0.00250 {
		t.Errorf("gpt-4o-2024-08-06 -> %v, want 0.00250", r.InputPer1k)
	}
	// Case insensitive
	r, _ = RateFor("GPT-4O")
	if r.InputPer1k != 0.00250 {
		t.Errorf("GPT-4O -> %v, want 0.00250", r.InputPer1k)
	}
}

func TestRateFor_Unknown_ReturnsDefault(t *testing.T) {
	r, key := RateFor("nonexistent-model")
	if key != "default" {
		t.Errorf("expected default key, got %q", key)
	}
	if r.InputPer1k != 0.001 || r.OutputPer1k != 0.003 {
		t.Errorf("default rate wrong: %+v", r)
	}
}

func TestRateFor_Empty_ReturnsDefault(t *testing.T) {
	r, key := RateFor("")
	if key != "default" {
		t.Errorf("expected default, got %q", key)
	}
	if r.InputPer1k != 0.001 {
		t.Errorf("default InputPer1k = %v, want 0.001", r.InputPer1k)
	}
}

func TestLookupRateForProvider_DoesNotInventUnknownPrice(t *testing.T) {
	if _, _, ok := LookupRateForProvider("anyrouter", "openai/nonexistent-model"); ok {
		t.Fatal("unknown custom-provider model must not receive the legacy default price")
	}
	rate, source, ok := LookupRateForProvider("openai", "gpt-4o")
	if !ok || source != "gpt-4o" || rate.InputPer1k != 0.0025 {
		t.Fatalf("known rate = %+v, %q, %v", rate, source, ok)
	}
}

func TestCostFor_Basic(t *testing.T) {
	// gpt-4o-mini: $0.15 input and $0.60 output per million.
	got := CostFor("gpt-4o-mini", 1000, 1000)
	if got != 0.00075 {
		t.Errorf("CostFor(1k in, 1k out) = %v, want 0.00075", got)
	}
	got = CostFor("gpt-4o-mini", 100, 50)
	if got != 0.000045 {
		t.Errorf("CostFor(100 in, 50 out) = %v, want 0.000045", got)
	}
}

func TestCostFor_Zero(t *testing.T) {
	if got := CostFor("gpt-4o", 0, 0); got != 0 {
		t.Errorf("zero tokens should be 0, got %v", got)
	}
	if got := CostFor("gpt-4o", 0, 1000); got != 0.010 {
		t.Errorf("output-only 1k gpt-4o = %v, want 0.010", got)
	}
	if got := CostFor("gpt-4o", 1000, 0); got != 0.0025 {
		t.Errorf("input-only 1k gpt-4o = %v, want 0.0025", got)
	}
}

func TestCostFor_UsesDefaultForUnknown(t *testing.T) {
	got := CostFor("garbage-model", 1000, 1000)
	if got != 0.004 {
		t.Errorf("CostFor(garbage, 1k, 1k) = %v, want 0.004", got)
	}
}

func TestCostAtRate_PricesCachedInputAsSubset(t *testing.T) {
	rate := perMillion(5, 0.5, 30)
	got, cacheKnown := CostAtRate(rate, 1_000_000, 100_000, 800_000)
	if !cacheKnown {
		t.Fatal("expected published cached-input price")
	}
	// 200k fresh input ($1) + 800k cached input ($0.40) + 100k output ($3).
	if got != 4.4 {
		t.Fatalf("cache-aware cost = %v, want 4.4", got)
	}
	got, cacheKnown = CostAtRate(perMillion(5, 0, 30), 1000, 0, 500)
	if cacheKnown || got != 0.005 {
		t.Fatalf("missing cache rate should use normal input price: got=%v known=%v", got, cacheKnown)
	}
}

func TestFormatUSD(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "$0.00"},
		{0.0001, "$0.0001"},
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
		"gpt-4o": {InputPer1k: 0.00125, OutputPer1k: 0.00500},
	})
	r, key := RateFor("gpt-4o")
	if r.InputPer1k != 0.00125 {
		t.Errorf("fetched gpt-4o input = %v, want 0.00125", r.InputPer1k)
	}
	if r.OutputPer1k != 0.00500 {
		t.Errorf("fetched gpt-4o output = %v, want 0.00500", r.OutputPer1k)
	}
	if key != "gpt-4o (fetched)" {
		t.Errorf("key = %q, want 'gpt-4o (fetched)'", key)
	}
	// CostFor should use the fetched rate.
	cost := CostFor("gpt-4o", 1000, 1000)
	want := 0.00625
	if cost != want {
		t.Errorf("CostFor with fetched rate = %v, want %v", cost, want)
	}
}

// F28: Unknown model with fetched rates still falls back to default.
func TestSetFetchedRates_UnknownStillDefault(t *testing.T) {
	defer SetFetchedRates(nil) // cleanup

	SetFetchedRates(map[string]Rate{
		"gpt-4o": {InputPer1k: 0.00125, OutputPer1k: 0.00500},
	})
	r, key := RateFor("unknown-model")
	if key != "default" {
		t.Errorf("expected default, got %q", key)
	}
	if r.InputPer1k != 0.001 {
		t.Errorf("default rate wrong: %v", r.InputPer1k)
	}
}

func TestSetFetchedRates_OpenRouterFullModelID(t *testing.T) {
	defer SetFetchedRates(nil)

	SetFetchedRates(map[string]Rate{
		"deepseek/deepseek-chat": {InputPer1k: 0.00007, OutputPer1k: 0.00027},
	})
	r, key := RateFor("deepseek/deepseek-chat")
	if r.InputPer1k != 0.00007 || r.OutputPer1k != 0.00027 {
		t.Fatalf("rate=%+v, want OpenRouter fetched deepseek rate", r)
	}
	if key != "deepseek/deepseek-chat (fetched)" {
		t.Fatalf("key=%q", key)
	}
}

func TestRateForProvider_UsesFetchedProviderModelKey(t *testing.T) {
	defer SetFetchedRates(nil)

	SetFetchedRates(map[string]Rate{
		"deepseek/deepseek-chat": {InputPer1k: 0.00007, OutputPer1k: 0.00027},
	})
	r, key := RateForProvider("deepseek", "deepseek-chat")
	if r.InputPer1k != 0.00007 || r.OutputPer1k != 0.00027 {
		t.Fatalf("rate=%+v, want fetched deepseek/deepseek-chat", r)
	}
	if key != "deepseek/deepseek-chat (fetched)" {
		t.Fatalf("key=%q", key)
	}
}

func TestRateFor_StripsRouterDisplaySuffix(t *testing.T) {
	r, key := RateFor("gpt-5.5 (2 accounts)")
	if key != "gpt-5.5" {
		t.Fatalf("key=%q want gpt-5.5", key)
	}
	if r.InputPer1k == modelRates["default"].InputPer1k && r.OutputPer1k == modelRates["default"].OutputPer1k {
		t.Fatalf("expected gpt-5.5 rate, got default %+v", r)
	}
}

// F28: SetFetchedRates(nil) restores hardcoded rates.
func TestSetFetchedRates_NilRestoresHardcoded(t *testing.T) {
	SetFetchedRates(map[string]Rate{
		"gpt-4o": {InputPer1k: 0.01, OutputPer1k: 0.01},
	})
	SetFetchedRates(nil)
	r, _ := RateFor("gpt-4o")
	if r.InputPer1k != 0.00250 {
		t.Errorf("after nil, gpt-4o input = %v, want 0.00250 (hardcoded)", r.InputPer1k)
	}
}

// F28: GetFetchedRates returns a copy.
func TestGetFetchedRates_ReturnsCopy(t *testing.T) {
	defer SetFetchedRates(nil) // cleanup

	SetFetchedRates(map[string]Rate{
		"gpt-4o": {InputPer1k: 0.00125},
	})
	got := GetFetchedRates()
	entry := got["gpt-4o"]
	entry.InputPer1k = 999
	got["gpt-4o"] = entry // write back (but should not matter)
	r, _ := RateFor("gpt-4o")
	if r.InputPer1k != 0.00125 {
		t.Errorf("mutation leaked into fetched rates: %v", r.InputPer1k)
	}
}

// Per-endpoint override wins over fetched and hardcoded rates.
func TestRateForProvider_EndpointOverride(t *testing.T) {
	defer SetProviderRates(nil)
	defer SetFetchedRates(nil)

	// Hardcoded gpt-4o = 2.50/10.00; fetched = 1.25/5.00.
	SetFetchedRates(map[string]Rate{
		"gpt-4o": {InputPer1k: 0.00125, OutputPer1k: 0.00500},
	})
	// Proxy "myproxy" prices gpt-4o cheaper still. Mixed-case key
	// must still match a lowercased lookup.
	SetProviderRates(map[string]Rate{
		"MyProxy/gpt-4o": {InputPer1k: 0.00050, OutputPer1k: 0.00100},
	})

	r, key := RateForProvider("myproxy", "gpt-4o")
	if r.InputPer1k != 0.00050 || r.OutputPer1k != 0.00100 {
		t.Errorf("endpoint override not applied: %+v", r)
	}
	if key != "myproxy/gpt-4o (endpoint)" {
		t.Errorf("key = %q, want 'myproxy/gpt-4o (endpoint)'", key)
	}

	// Cost uses the override.
	if got := CostForProvider("myproxy", "gpt-4o", 1000, 1000); got != 0.00150 {
		t.Errorf("CostForProvider with override = %v, want 0.00150", got)
	}

	// A different provider falls through to the fetched rate.
	r, _ = RateForProvider("openai", "gpt-4o")
	if r.InputPer1k != 0.00125 {
		t.Errorf("other provider should use fetched rate, got %v", r.InputPer1k)
	}

	// No provider behaves like plain RateFor (fetched rate).
	r, _ = RateFor("gpt-4o")
	if r.InputPer1k != 0.00125 {
		t.Errorf("RateFor should still use fetched rate, got %v", r.InputPer1k)
	}
}

// Provider override for an unconfigured model falls through cleanly.
func TestRateForProvider_NoOverrideFallsThrough(t *testing.T) {
	defer SetProviderRates(nil)
	SetProviderRates(map[string]Rate{
		"myproxy/gpt-4o": {InputPer1k: 0.00050, OutputPer1k: 0.00100},
	})
	// Same proxy, different model -> hardcoded gpt-4o-mini rate.
	r, key := RateForProvider("myproxy", "gpt-4o-mini")
	if r.InputPer1k != 0.00015 || key != "gpt-4o-mini" {
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
