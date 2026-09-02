package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRequestBudgetCountsAndPersists(t *testing.T) {
	ResetRequestBudgetForTest()
	defer ResetRequestBudgetForTest()

	dir := t.TempDir()
	InitRequestBudget(dir)

	const zen = "https://opencode.ai/zen/v1"
	noteProviderRequest(zen)
	noteProviderRequest(zen)
	noteProviderRequest("http://localhost:1234/v1")

	if got := ProviderRequestsToday(zen); got != 2 {
		t.Fatalf("zen today = %d, want 2", got)
	}
	if got := ProviderRequestsToday("http://localhost:1234/v1"); got != 1 {
		t.Fatalf("local today = %d, want 1", got)
	}

	// Persistence: a fresh process (new Init on same dir) sees the counts.
	ResetRequestBudgetForTest()
	InitRequestBudget(dir)
	if got := ProviderRequestsToday(zen); got != 2 {
		t.Fatalf("after reload zen today = %d, want 2", got)
	}
}

func TestRequestBudgetUTCRollover(t *testing.T) {
	ResetRequestBudgetForTest()
	defer ResetRequestBudgetForTest()

	dir := t.TempDir()
	path := filepath.Join(dir, RequestBudgetFileName)
	yesterday := "2026-01-01"
	if err := os.WriteFile(path, []byte(`{"day":"`+yesterday+`","counts":{"https://opencode.ai/zen/v1":100}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	InitRequestBudget(dir)
	const zen = "https://opencode.ai/zen/v1"
	if got := ProviderRequestsToday(zen); got != 0 {
		t.Fatalf("stale day must read as 0, got %d", got)
	}
	noteProviderRequest(zen)
	if got := ProviderRequestsToday(zen); got != 1 {
		t.Fatalf("fresh day count = %d, want 1", got)
	}

	// The file must now carry the new day, not the stale one.
	data, _ := os.ReadFile(path)
	var saved struct {
		Day string `json:"day"`
	}
	if json.Unmarshal(data, &saved) != nil || saved.Day == yesterday {
		t.Fatalf("rollover not persisted: day=%q err=%v", saved.Day, json.Unmarshal(data, &saved))
	}
}

func TestRequestBudgetUninitializedIsNoop(t *testing.T) {
	ResetRequestBudgetForTest()
	defer ResetRequestBudgetForTest()

	noteProviderRequest("https://opencode.ai/zen/v1") // must not panic
	if got := ProviderRequestsToday("https://opencode.ai/zen/v1"); got != 0 {
		t.Fatalf("uninitialized counter = %d, want 0", got)
	}
}

func TestProviderRequestsTodayFlexible(t *testing.T) {
	ResetRequestBudgetForTest()
	defer ResetRequestBudgetForTest()

	dir := t.TempDir()
	InitRequestBudget(dir)
	const zen = "https://opencode.ai/zen/v1"
	noteProviderRequest(zen)
	noteProviderRequest(zen)
	noteProviderRequest("http://localhost:1234/v1")

	// Exact URL match.
	if got := ProviderRequestsTodayFlexible(zen); got != 2 {
		t.Fatalf("exact url = %d, want 2", got)
	}
	// Bare host (what session records / UIs often hold).
	if got := ProviderRequestsTodayFlexible("opencode.ai"); got != 2 {
		t.Fatalf("bare host = %d, want 2", got)
	}
	if got := ProviderRequestsTodayFlexible("https://opencode.ai/some/other/path"); got != 2 {
		t.Fatalf("same host other path = %d, want 2", got)
	}
	// Unknown host stays zero; localhost must not bleed into it.
	if got := ProviderRequestsTodayFlexible("https://api.example.com/v1"); got != 0 {
		t.Fatalf("unknown host = %d, want 0", got)
	}
}
