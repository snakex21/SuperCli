package llm

import (
	"testing"
	"time"
)

func TestPrefillProfilesExtremeCallActivatesScopedBudget(t *testing.T) {
	store := LoadPrefillProfiles(t.TempDir())
	profile, changed := store.Observe("connection-a", "model", PrefillSample{
		InputTokens: 40_000,
		TTFT:        30 * time.Second,
	})
	if !changed || profile.BudgetTokens != 20_000 {
		t.Fatalf("profile = %+v changed=%v, want 20k active budget", profile, changed)
	}
	if budget, ok := store.Budget("connection-a", "model", 100_000); !ok || budget != 20_000 {
		t.Fatalf("Budget = %d,%v", budget, ok)
	}
	if _, ok := store.Budget("connection-b", "model", 100_000); ok {
		t.Fatal("budget leaked across provider connections")
	}
}

func TestPrefillProfilesAccountsForCachedPrefixAndPersists(t *testing.T) {
	dir := t.TempDir()
	store := LoadPrefillProfiles(dir)
	profile, _ := store.Observe("remote-http", "model", PrefillSample{
		InputTokens:  40_000,
		CachedTokens: 10_000,
		TTFT:         30 * time.Second,
	})
	if profile.BudgetTokens != 25_000 || profile.LastEvaluated != 30_000 {
		t.Fatalf("cached-aware profile = %+v", profile)
	}
	if profile.LastTokensPerS < 999 || profile.LastTokensPerS > 1001 {
		t.Fatalf("tokens/s = %f, want about 1000", profile.LastTokensPerS)
	}
	reloaded := LoadPrefillProfiles(dir)
	got, ok := reloaded.Profile("remote-http", "model")
	if !ok || got.BudgetTokens != 25_000 || got.LastEvaluated != 30_000 {
		t.Fatalf("reloaded profile = %+v ok=%v", got, ok)
	}
}

func TestPrefillProfilesCacheEffectiveCallsClearBudget(t *testing.T) {
	store := LoadPrefillProfiles(t.TempDir())
	store.Observe("p", "m", PrefillSample{InputTokens: 40_000, TTFT: 30 * time.Second})
	if _, ok := store.Budget("p", "m", 200_000); !ok {
		t.Fatal("uncached extreme call should activate a budget")
	}
	// Slow because of thinking/network, but 95% cached: not a prefill limit.
	profile, changed := store.Observe("p", "m", PrefillSample{InputTokens: 80_000, CachedTokens: 76_000, TTFT: 40 * time.Second})
	if !changed || profile.BudgetTokens != 0 || profile.SlowSamples != 0 || profile.LastTTFTMS != 40_000 {
		t.Fatalf("cache-effective call kept budget: %+v changed=%v", profile, changed)
	}
	if _, ok := store.Budget("p", "m", 200_000); ok {
		t.Fatal("budget still active after cache-effective call")
	}
}

func TestPrefillProfilesFastProbeDoesNotActivateBudget(t *testing.T) {
	store := LoadPrefillProfiles(t.TempDir())
	profile, changed := store.Observe("p", "m", PrefillSample{
		InputTokens: 40_000,
		TTFT:        5 * time.Second,
	})
	if changed || profile.BudgetTokens != 0 {
		t.Fatalf("fast call unexpectedly constrained prompt: %+v", profile)
	}
}
