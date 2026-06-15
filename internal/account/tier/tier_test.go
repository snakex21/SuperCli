package tier

import "testing"

func TestClassifyParams(t *testing.T) {
	cases := map[string]Tier{
		"qwen3.5-9b":      Small, // 9B < 12B
		"qwen3.5-35b-a3b": Small, // MoE: active 3B wins over 35B
		"qwen3.6-35b-a3b": Small,
		"qwen3.6-27b":     Big, // 27B >= 12B
		"llama-3.1-8b":    Small,
		"llama-3.1-70b":   Big,
		"mistral-1.5b":    Small,
	}
	for model, want := range cases {
		if got := Classify(model, 0, 0, nil); got != want {
			t.Errorf("Classify(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestClassifyMarkers(t *testing.T) {
	for _, m := range []string{"gpt-5-mini", "gemini-2.5-flash", "claude-haiku-4", "some-nano", "model_lite", "tiny-model"} {
		if got := Classify(m, 0, 0, nil); got != Small {
			t.Errorf("Classify(%q) = %v, want small (marker)", m, got)
		}
	}
	// "gemini" must NOT match the "mini" marker as a substring;
	// no params, no price → unknown → small anyway, but make
	// sure hasMarker itself is token-based.
	if hasMarker("gemini-2.5-pro") {
		t.Error("hasMarker(gemini-2.5-pro) = true; substring match leaked")
	}
}

func TestClassifyPrice(t *testing.T) {
	if got := Classify("whatever-200b", 0.5, 1.0, nil); got != Small {
		t.Errorf("cheap priced model = %v, want small (price beats params)", got)
	}
	if got := Classify("cheapname-mini", 3, 15, nil); got != Big {
		t.Errorf("expensive priced model = %v, want big (price beats marker)", got)
	}
}

func TestClassifyGlobOverride(t *testing.T) {
	rules := []Rule{
		{Pattern: "qwen3.6-*", Tier: "big"},
		{Pattern: "*", Tier: "small"},
	}
	if got := Classify("QWEN3.6-1b", 0, 0, rules); got != Big {
		t.Errorf("glob override (case-insensitive) = %v, want big", got)
	}
	if got := Classify("gpt-5.2", 10, 30, rules); got != Small {
		t.Errorf("catch-all glob = %v, want small (rules beat price)", got)
	}
	// First match wins.
	rules2 := []Rule{
		{Pattern: "foo-*", Tier: "small"},
		{Pattern: "foo-bar", Tier: "big"},
	}
	if got := Classify("foo-bar", 0, 0, rules2); got != Small {
		t.Errorf("first-match-wins = %v, want small", got)
	}
}

func TestClassifyUnknown(t *testing.T) {
	if got := Classify("mystery-model", 0, 0, nil); got != Small {
		t.Errorf("unknown model = %v, want small", got)
	}
}

func TestParseParams(t *testing.T) {
	if b, ok := ParseParams("qwen3.5-35b-a3b"); !ok || b != 3 {
		t.Errorf("ParseParams MoE = (%v, %v), want (3, true)", b, ok)
	}
	if b, ok := ParseParams("qwen3.6-27b"); !ok || b != 27 {
		t.Errorf("ParseParams = (%v, %v), want (27, true)", b, ok)
	}
	if _, ok := ParseParams("gpt-5-mini"); ok {
		t.Error("ParseParams(gpt-5-mini) ok = true, want false")
	}
	if b, ok := ParseParams("phi-1.5b-instruct"); !ok || b != 1.5 {
		t.Errorf("ParseParams fractional = (%v, %v), want (1.5, true)", b, ok)
	}
}
