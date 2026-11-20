package config

import "testing"

func TestParseMaxParallelAgents(t *testing.T) {
	for in, want := range map[string]int{"0": 0, "unlimited": 0, "bez limitu": 0, "∞": 0, "3": 3, " 12 ": 12} {
		got, err := ParseMaxParallelAgents(in)
		if err != nil || got == nil || *got != want {
			t.Errorf("ParseMaxParallelAgents(%q) = %v, %v; want %d", in, got, err, want)
		}
	}
	if got, err := ParseMaxParallelAgents(""); err != nil || got != nil {
		t.Errorf("empty = %v, %v; want default (nil)", got, err)
	}
	for _, bad := range []string{"-1", "many", "2.5"} {
		if _, err := ParseMaxParallelAgents(bad); err == nil {
			t.Errorf("ParseMaxParallelAgents(%q) accepted", bad)
		}
	}
	if EffectiveMaxParallelAgents(nil) != DefaultMaxParallelAgents || DefaultMaxParallelAgents != 6 {
		t.Fatal("default must stay 6")
	}
	zero, nine, four := 0, 9, 4
	if !ParallelAgentsHigh(&zero) || !ParallelAgentsHigh(&nine) || ParallelAgentsHigh(&four) || ParallelAgentsHigh(nil) {
		t.Fatal("warning threshold: unlimited or > 8 only")
	}
}

func TestProviderAgentLimitsRoundTrip(t *testing.T) {
	m, err := ParseProviderAgentLimits(" lmstudio=1; openrouter = 4 ,codex=unlimited")
	if err != nil {
		t.Fatal(err)
	}
	if m["lmstudio"] != 1 || m["openrouter"] != 4 || m["codex"] != 0 || len(m) != 3 {
		t.Fatalf("limits = %v", m)
	}
	if got := FormatProviderAgentLimits(m); got != "codex=0; lmstudio=1; openrouter=4" {
		t.Fatalf("format = %q", got)
	}
	if m, err := ParseProviderAgentLimits(""); err != nil || m != nil {
		t.Fatalf("empty = %v, %v", m, err)
	}
	for _, bad := range []string{"lmstudio", "=2", "x=-1"} {
		if _, err := ParseProviderAgentLimits(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestMergeKeepsParallelAgentSettings(t *testing.T) {
	n := 0
	var dst TomlConfig
	mergeToml(&dst, TomlConfig{MaxParallelAgents: &n, MaxParallelAgentsPerProvider: map[string]int{"lmstudio": 1}})
	if dst.MaxParallelAgents == nil || *dst.MaxParallelAgents != 0 || dst.MaxParallelAgentsPerProvider["lmstudio"] != 1 {
		t.Fatalf("merged = %+v", dst)
	}
}
