package llm

import "testing"

func TestCapabilityRegistry_Empty(t *testing.T) {
	r := NewCapabilityRegistry()
	if r.Len() != 0 {
		t.Errorf("Len = %d, want 0", r.Len())
	}
	if r.Models()[0:] != nil && len(r.Models()) != 0 {
		t.Errorf("Models() = %v, want empty", r.Models())
	}
}

func TestCapabilityRegistry_RegisterAndGet(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{
		ID: "x", Vision: true, ToolUse: true, Stream: true,
		Source: SourceCatalog,
	})
	if got, ok := r.Get("x"); !ok || !got.Vision {
		t.Fatalf("Get(x) = %+v, %v", got, ok)
	}
}

func TestCapabilityRegistry_HasVision(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "v1", Vision: true, ToolUse: true, Stream: true, Source: SourceSeed})
	r.Register(ModelInfo{ID: "v0", Vision: false, ToolUse: true, Stream: true, Source: SourceSeed})
	cases := []struct {
		model string
		want  bool
	}{
		{"v1", true},
		{"v0", false},
		{"unknown-model", false},
	}
	for _, c := range cases {
		t.Run(c.model, func(t *testing.T) {
			if got := r.HasVision(c.model); got != c.want {
				t.Fatalf("HasVision(%q) = %v, want %v", c.model, got, c.want)
			}
		})
	}
}

func TestCapabilityRegistry_AllowsVisionAttemptForUnknownProviderMetadata(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "dynamic-unknown", Source: SourceProvider})
	r.Register(ModelInfo{ID: "dynamic-text", VisionKnown: true, Source: SourceProvider})
	r.Register(ModelInfo{ID: "dynamic-vision", Vision: true, VisionKnown: true, Source: SourceProvider})
	r.Register(ModelInfo{ID: "curated-text", Source: SourceSeed})

	if !r.AllowsVisionAttempt("dynamic-unknown") {
		t.Fatal("provider model without modality metadata should be tried optimistically")
	}
	if r.AllowsVisionAttempt("dynamic-text") {
		t.Fatal("authoritative text-only metadata should block image input")
	}
	if !r.AllowsVisionAttempt("dynamic-vision") {
		t.Fatal("advertised vision model should accept image input")
	}
	if r.AllowsVisionAttempt("curated-text") || r.AllowsVisionAttempt("missing") {
		t.Fatal("curated text and missing models should remain strict")
	}
}

func TestCapabilityRegistry_HasToolUse(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "t1", ToolUse: true, Stream: true, Source: SourceSeed})
	r.Register(ModelInfo{ID: "t0", ToolUse: false, Stream: true, Source: SourceSeed})
	if !r.HasToolUse("t1") {
		t.Error("t1 should have tool use")
	}
	if r.HasToolUse("t0") {
		t.Error("t0 should not have tool use")
	}
	if r.HasToolUse("unknown") {
		t.Error("unknown should not have tool use")
	}
}

func TestCapabilityRegistry_HasReasoning(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "o1", Reasoning: true, Stream: true, Source: SourceSeed})
	r.Register(ModelInfo{ID: "gpt-4o", Reasoning: false, Stream: true, Source: SourceSeed})
	if !r.HasReasoning("o1") {
		t.Error("o1 should have reasoning")
	}
	if r.HasReasoning("gpt-4o") {
		t.Error("gpt-4o should not have reasoning")
	}
	if r.HasReasoning("unknown") {
		t.Error("unknown should not have reasoning")
	}
}

func TestCapabilityRegistry_Provider(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "gpt-4o", Provider: "openai", Source: SourceSeed})
	if r.Provider("gpt-4o") != "openai" {
		t.Errorf("Provider = %q", r.Provider("gpt-4o"))
	}
	if r.Provider("unknown") != "" {
		t.Error("unknown should be empty provider")
	}
}

func TestCapabilityRegistry_RegisterAll_NoDowngrade(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "x", Vision: true, Source: SourceProbe})
	// Seed tries to overwrite probe — should NOT.
	r.RegisterAll([]ModelInfo{{ID: "x", Vision: false, Source: SourceSeed}})
	if !r.HasVision("x") {
		t.Error("probe (vision=true) should survive seed overwrite attempt")
	}
}

func TestCapabilityRegistry_RegisterAll_Upgrade(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "x", Vision: false, Source: SourceSeed})
	// Provider overrides seed.
	r.RegisterAll([]ModelInfo{{ID: "x", Vision: true, Source: SourceProvider}})
	if !r.HasVision("x") {
		t.Error("provider should have upgraded vision flag")
	}
}

func TestCapabilityRegistry_RegisterAll_DuplicateEntries(t *testing.T) {
	r := NewCapabilityRegistry()
	r.RegisterAll([]ModelInfo{
		{ID: "a", Vision: false, Source: SourceSeed},
		{ID: "a", Vision: true, Source: SourceProvider},
		{ID: "a", Vision: false, Source: SourceCatalog}, // catalog cannot override provider
	})
	got, _ := r.Get("a")
	if !got.Vision {
		t.Error("vision should be true (provider won)")
	}
}

func TestCapabilityRegistry_Models_Sorted(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "zebra", Source: SourceSeed})
	r.Register(ModelInfo{ID: "alpha", Source: SourceSeed})
	r.Register(ModelInfo{ID: "mike", Source: SourceSeed})
	got := r.Models()
	want := []string{"alpha", "mike", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCapabilityRegistry_All_Sorted(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "b", Source: SourceSeed})
	r.Register(ModelInfo{ID: "a", Source: SourceSeed})
	all := r.All()
	if len(all) != 2 || all[0].ID != "a" {
		t.Errorf("All() = %+v", all)
	}
}

func TestCapabilityRegistry_Capabilities_BackwardCompat(t *testing.T) {
	m := ModelInfo{Vision: true, ToolUse: true, Stream: false}
	c := m.Capabilities()
	if !c.Vision || !c.ToolUse || c.Stream {
		t.Errorf("Capabilities = %+v", c)
	}
}

func TestCapabilityRegistry_ConcurrentSafe(t *testing.T) {
	r := NewCapabilityRegistry()
	const n = 100
	done := make(chan struct{}, 2)
	go func() {
		for i := 0; i < n; i++ {
			r.Register(ModelInfo{ID: "x", Source: SourceSeed})
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < n; i++ {
			r.HasVision("x")
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}

func TestLoadSeed_PopulatesRegistry(t *testing.T) {
	r := NewCapabilityRegistry()
	seed, err := LoadSeed()
	if err != nil {
		t.Fatal(err)
	}
	r.RegisterAll(seed)
	// Some known seed entries.
	for _, id := range []string{"gpt-4o", "claude-3-5-sonnet-latest", "o1", "deepseek-r1"} {
		if !r.HasVision(id) && id != "o1" && id != "deepseek-r1" {
			// o1 and deepseek-r1 are text-only
			continue
		}
		if _, ok := r.Get(id); !ok {
			t.Errorf("seed should contain %q", id)
		}
	}
}

func TestCapabilityRegistry_IsConfigured(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "configured", Source: SourceSeed})
	if !r.IsConfigured("configured") {
		t.Error("seed model should be configured")
	}
	if r.IsConfigured("nope") {
		t.Error("unknown model should not be configured")
	}
}

func TestSuggestCheapestForTask_PicksLowestCost(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "expensive", ToolUse: true, Provider: "openai", InputCost: 10, OutputCost: 30, Source: SourceSeed})
	r.Register(ModelInfo{ID: "cheap", ToolUse: true, Provider: "openai", InputCost: 0.15, OutputCost: 0.6, Source: SourceSeed})
	r.Register(ModelInfo{ID: "main", ToolUse: true, Provider: "openai", InputCost: 5, OutputCost: 15, Source: SourceSeed})
	got, ok := r.SuggestCheapestForTask("plan", "main")
	if !ok {
		t.Fatal("expected a suggestion")
	}
	if got != "cheap" {
		t.Errorf("got %q, want cheap (lowest cost)", got)
	}
}

func TestSuggestCheapestForTask_ExcludesMainModel(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "only", ToolUse: true, Provider: "openai", InputCost: 1, OutputCost: 1, Source: SourceSeed})
	// main == only → must NOT be suggested as draft of itself.
	if _, ok := r.SuggestCheapestForTask("plan", "only"); ok {
		t.Error("should not suggest the main model as its own draft")
	}
}

func TestSuggestCheapestForTask_RequiresToolUse(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "chat-only", ToolUse: false, Provider: "openai", InputCost: 0.1, OutputCost: 0.1, Source: SourceSeed})
	r.Register(ModelInfo{ID: "tools", ToolUse: true, Provider: "openai", InputCost: 5, OutputCost: 5, Source: SourceSeed})
	r.Register(ModelInfo{ID: "main", ToolUse: true, Provider: "openai", InputCost: 5, OutputCost: 5, Source: SourceSeed})
	got, ok := r.SuggestCheapestForTask("plan", "main")
	if !ok {
		t.Fatal("expected suggestion")
	}
	if got != "tools" {
		t.Errorf("got %q, want tools (chat-only has no ToolUse)", got)
	}
}

func TestSuggestCheapestForTask_RequiresProvider(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "orphan", ToolUse: true, InputCost: 0.1, OutputCost: 0.1, Source: SourceSeed})
	r.Register(ModelInfo{ID: "ok", ToolUse: true, Provider: "openai", InputCost: 5, OutputCost: 5, Source: SourceSeed})
	r.Register(ModelInfo{ID: "main", ToolUse: true, Provider: "openai", InputCost: 5, OutputCost: 5, Source: SourceSeed})
	got, ok := r.SuggestCheapestForTask("plan", "main")
	if !ok {
		t.Fatal("expected suggestion")
	}
	if got != "ok" {
		t.Errorf("got %q, want ok (orphan has no provider)", got)
	}
}

func TestSuggestCheapestForTask_FallbackToNoCost(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "alpha-no-cost", ToolUse: true, Provider: "openai", Source: SourceSeed})
	r.Register(ModelInfo{ID: "zeta-no-cost", ToolUse: true, Provider: "openai", Source: SourceSeed})
	r.Register(ModelInfo{ID: "main", ToolUse: true, Provider: "openai", InputCost: 5, OutputCost: 5, Source: SourceSeed})
	got, ok := r.SuggestCheapestForTask("plan", "main")
	if !ok {
		t.Fatal("expected suggestion")
	}
	// Alphabetical first.
	if got != "alpha-no-cost" {
		t.Errorf("got %q, want alpha-no-cost (alphabetical first among no-cost)", got)
	}
}

func TestSuggestCheapestForTask_PrefersKnownCostOverZero(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "no-cost", ToolUse: true, Provider: "openai", Source: SourceSeed})
	r.Register(ModelInfo{ID: "expensive-but-known", ToolUse: true, Provider: "openai", InputCost: 100, OutputCost: 100, Source: SourceSeed})
	r.Register(ModelInfo{ID: "main", ToolUse: true, Provider: "openai", InputCost: 5, OutputCost: 5, Source: SourceSeed})
	got, ok := r.SuggestCheapestForTask("plan", "main")
	if !ok {
		t.Fatal("expected suggestion")
	}
	if got != "expensive-but-known" {
		t.Errorf("got %q, want expensive-but-known (known cost wins over cost=0)", got)
	}
}

func TestSuggestCheapestForTask_NoCandidate(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "main", ToolUse: true, Provider: "openai", Source: SourceSeed})
	if id, ok := r.SuggestCheapestForTask("plan", "main"); ok {
		t.Errorf("got (%q, true), want (_, false) for empty registry", id)
	}
}

func TestSuggestCheapestN_ReturnsSortedCheapestFirst(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "main", ToolUse: true, Provider: "openai", Source: SourceSeed})
	r.Register(ModelInfo{ID: "expensive", ToolUse: true, Provider: "x", Source: SourceSeed, InputCost: 5, OutputCost: 5})
	r.Register(ModelInfo{ID: "cheap", ToolUse: true, Provider: "x", Source: SourceSeed, InputCost: 0.1, OutputCost: 0.1})
	r.Register(ModelInfo{ID: "mid", ToolUse: true, Provider: "x", Source: SourceSeed, InputCost: 1, OutputCost: 1})
	got := r.SuggestCheapestN("plan", "main", 3)
	want := []string{"cheap", "mid", "expensive"}
	if !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSuggestCheapestN_ClampsToAvailable(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "main", ToolUse: true, Provider: "openai", Source: SourceSeed})
	r.Register(ModelInfo{ID: "a", ToolUse: true, Provider: "x", Source: SourceSeed, InputCost: 0.1, OutputCost: 0.1})
	got := r.SuggestCheapestN("plan", "main", 10)
	if len(got) != 1 {
		t.Errorf("got %d, want 1 (clamped to available)", len(got))
	}
}

func TestSuggestCheapestN_ExcludesMain(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "main", ToolUse: true, Provider: "openai", Source: SourceSeed, InputCost: 0, OutputCost: 0})
	got := r.SuggestCheapestN("plan", "main", 5)
	for _, id := range got {
		if id == "main" {
			t.Errorf("main should be excluded, got %v", got)
		}
	}
}

func TestSuggestCheapestN_ZeroNReturnsNil(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "a", ToolUse: true, Provider: "x", Source: SourceSeed, InputCost: 1, OutputCost: 1})
	if got := r.SuggestCheapestN("plan", "main", 0); got != nil {
		t.Errorf("n=0 should return nil, got %v", got)
	}
	if got := r.SuggestCheapestN("plan", "main", -1); got != nil {
		t.Errorf("n<0 should return nil, got %v", got)
	}
}

func TestSuggestCheapestN_FallbackToNoCost(t *testing.T) {
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "main", ToolUse: true, Provider: "openai", Source: SourceSeed})
	r.Register(ModelInfo{ID: "free-a", ToolUse: true, Provider: "x", Source: SourceSeed}) // no cost
	r.Register(ModelInfo{ID: "free-b", ToolUse: true, Provider: "x", Source: SourceSeed}) // no cost
	r.Register(ModelInfo{ID: "priced", ToolUse: true, Provider: "x", Source: SourceSeed, InputCost: 1, OutputCost: 1})
	got := r.SuggestCheapestN("plan", "main", 3)
	// priced goes first, then free-a, free-b alphabetically
	want := []string{"priced", "free-a", "free-b"}
	if !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
