package llm

import (
	"testing"
)

func TestLoadSeed_NotEmpty(t *testing.T) {
	seed, err := LoadSeed()
	if err != nil {
		t.Fatal(err)
	}
	if len(seed) < 10 {
		t.Errorf("seed has %d entries, want >= 10", len(seed))
	}
}

func TestLoadSeed_AllValid(t *testing.T) {
	seed, err := LoadSeed()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range seed {
		if m.ID == "" {
			t.Error("entry with empty id")
		}
		if m.InputCost < 0 {
			t.Errorf("%s: input_cost < 0", m.ID)
		}
		if m.OutputCost < 0 {
			t.Errorf("%s: output_cost < 0", m.ID)
		}
		if m.ContextLength < 0 {
			t.Errorf("%s: context_length < 0", m.ID)
		}
		if m.Source != SourceSeed {
			t.Errorf("%s: source = %q, want %q", m.ID, m.Source, SourceSeed)
		}
	}
}

func TestLoadSeed_NoDuplicates(t *testing.T) {
	seed, err := LoadSeed()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, m := range seed {
		if seen[m.ID] {
			t.Errorf("duplicate id %q", m.ID)
		}
		seen[m.ID] = true
	}
}

func TestLoadSeed_Sorted(t *testing.T) {
	seed, err := LoadSeed()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(seed); i++ {
		if seed[i-1].ID >= seed[i].ID {
			t.Errorf("not sorted at %d: %q >= %q", i, seed[i-1].ID, seed[i].ID)
		}
	}
}

func TestLoadSeed_DefaultsForMissing(t *testing.T) {
	// Every entry should have Stream=true (modern
	// endpoints stream by default).
	seed, err := LoadSeed()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range seed {
		if !m.Stream {
			t.Errorf("%s: Stream should default to true", m.ID)
		}
	}
}

func TestLoadSeed_StableID(t *testing.T) {
	// Calling LoadSeed() twice returns identical
	// entries (no mutation across calls).
	first, err := LoadSeed()
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadSeed()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("length changed: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Errorf("entry %d id changed: %q vs %q", i, first[i].ID, second[i].ID)
		}
	}
}
