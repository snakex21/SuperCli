package pricing

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"supercli/internal/account/credits"
	"supercli/internal/llm"
)

func closePrice(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

// ── parsePricePerToken tests ──

func TestParsePricePerToken(t *testing.T) {
	input := `[
		{"model":"gpt-4o","input_price":2.50,"output_price":10.00},
		{"model":"claude-3.5-sonnet","input_price":3.00,"output_price":15.00},
		{"model":"","input_price":0,"output_price":0}
	]`
	entries, err := parsePricePerToken([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].ModelID != "gpt-4o" {
		t.Errorf("model[0] = %q, want gpt-4o", entries[0].ModelID)
	}
	if entries[0].InputPer1M != 2.50 {
		t.Errorf("gpt-4o input = %v, want 2.50", entries[0].InputPer1M)
	}
	if entries[0].OutputPer1M != 10.00 {
		t.Errorf("gpt-4o output = %v, want 10.00", entries[0].OutputPer1M)
	}
	if entries[0].Source != "pricepertoken" {
		t.Errorf("source = %q, want pricepertoken", entries[0].Source)
	}
	if entries[0].FetchedAt.IsZero() {
		t.Error("FetchedAt should not be zero")
	}
}

func TestParsePricePerToken_Empty(t *testing.T) {
	entries, err := parsePricePerToken([]byte(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0, got %d", len(entries))
	}
}

func TestParsePricePerToken_Invalid(t *testing.T) {
	_, err := parsePricePerToken([]byte(`not json`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ── parseOpenRouter tests ──

func TestParseOpenRouter(t *testing.T) {
	input := `{
		"data": [
			{"id":"openai/gpt-4o","context_length":128000,"pricing":{"prompt":"0.0000025","input_cache_read":"0.00000125","completion":"0.00001"}},
			{"id":"anthropic/claude-3.5-sonnet","context_length":"200000","pricing":{"prompt":"0.000003","completion":"0.000015"}},
			{"id":"free-model","pricing":{}}
		]
	}`
	entries, err := parseOpenRouter([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	// Free model (both prices 0) should be excluded.
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (free excluded), got %d", len(entries))
	}
	// Check per-token → per-1M conversion.
	if entries[0].InputPer1M != 2.5 {
		t.Errorf("gpt-4o input = %v, want 2.5", entries[0].InputPer1M)
	}
	if entries[0].CachedInputPer1M != 1.25 {
		t.Errorf("gpt-4o cached input = %v, want 1.25", entries[0].CachedInputPer1M)
	}
	if entries[0].OutputPer1M != 10.0 {
		t.Errorf("gpt-4o output = %v, want 10.0", entries[0].OutputPer1M)
	}
	if entries[0].Source != "openrouter" {
		t.Errorf("source = %q, want openrouter", entries[0].Source)
	}
	if entries[0].ContextLength != 128000 || entries[1].ContextLength != 200000 {
		t.Errorf("context lengths = %d/%d, want 128000/200000", entries[0].ContextLength, entries[1].ContextLength)
	}
}

func TestParseOpenRouter_Empty(t *testing.T) {
	entries, err := parseOpenRouter([]byte(`{"data":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0, got %d", len(entries))
	}
}

func TestParseOpenRouter_Invalid(t *testing.T) {
	_, err := parseOpenRouter([]byte(`{bad`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseOpenRouter_MissingPricing(t *testing.T) {
	input := `{"data":[{"id":"test-model"}]}`
	entries, err := parseOpenRouter([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	// No pricing fields → both 0 → excluded.
	if len(entries) != 0 {
		t.Errorf("expected 0, got %d", len(entries))
	}
}

func TestParseOpenRouter_ContextOnlyFromTopProvider(t *testing.T) {
	input := `{
		"data": [
			{"id":"openrouter/fusion","pricing":{"prompt":"-1","completion":"-1"},"top_provider":{"context_length":1000000}}
		]
	}`
	entries, err := parseOpenRouter([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 context-only entry", len(entries))
	}
	if entries[0].ContextLength != 1000000 {
		t.Fatalf("context = %d, want 1000000", entries[0].ContextLength)
	}
	if entries[0].InputPer1M != 0 || entries[0].OutputPer1M != 0 {
		t.Fatalf("prices = %v/%v, want zero", entries[0].InputPer1M, entries[0].OutputPer1M)
	}
}

func TestFetchAll_MergesOpenRouterContextIntoEarlierPrice(t *testing.T) {
	now := time.Now().UTC()
	fetcher := &Fetcher{
		client: &http.Client{},
		sources: []Source{
			staticSource{name: "pricepertoken", entries: []PriceEntry{{
				ModelID:     "deepseek/deepseek-v4-flash",
				InputPer1M:  90,
				OutputPer1M: 180,
				Source:      "pricepertoken",
				FetchedAt:   now,
			}}},
			staticSource{name: "openrouter", entries: []PriceEntry{{
				ModelID:       "deepseek/deepseek-v4-flash",
				InputPer1M:    98,
				OutputPer1M:   196,
				ContextLength: 1048576,
				Source:        "openrouter",
				FetchedAt:     now.Add(time.Second),
			}}},
		},
	}

	entries := fetcher.FetchAll()
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 merged entry", len(entries))
	}
	if entries[0].InputPer1M != 90 || entries[0].OutputPer1M != 180 {
		t.Fatalf("prices = %v/%v, want first source priority", entries[0].InputPer1M, entries[0].OutputPer1M)
	}
	if entries[0].ContextLength != 1048576 {
		t.Fatalf("context = %d, want OpenRouter context", entries[0].ContextLength)
	}
}

// ── parseFlexFloat tests ──

func TestParseFlexFloat(t *testing.T) {
	cases := []struct {
		input string
		want  float64
		ok    bool
	}{
		{"0.0025", 0.0025, true},
		{"", 0, true},
		{"1.5e-4", 0.00015, true},
		{"abc", 0, false},
	}
	for _, tc := range cases {
		got, err := parseFlexFloat(tc.input)
		if tc.ok && err != nil {
			t.Errorf("parseFlexFloat(%q): unexpected error: %v", tc.input, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("parseFlexFloat(%q): expected error", tc.input)
		}
		if tc.ok && got != tc.want {
			t.Errorf("parseFlexFloat(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// ── Cache round-trip tests ──

func TestCacheRoundTrip(t *testing.T) {
	home := t.TempDir()
	entries := []PriceEntry{
		{ModelID: "gpt-4o", InputPer1M: 2.5, OutputPer1M: 10.0, Source: "test", FetchedAt: time.Now().UTC()},
		{ModelID: "claude-3.5", InputPer1M: 3.0, OutputPer1M: 15.0, Source: "test", FetchedAt: time.Now().UTC()},
	}
	if err := SaveCache(home, entries); err != nil {
		t.Fatal(err)
	}
	loaded := LoadCache(home)
	if len(loaded) != 2 {
		t.Fatalf("expected 2, got %d", len(loaded))
	}
	if loaded[0].ModelID != "gpt-4o" {
		t.Errorf("model[0] = %q, want gpt-4o", loaded[0].ModelID)
	}
}

func TestLoadCache_Missing(t *testing.T) {
	home := t.TempDir()
	loaded := LoadCache(home)
	if loaded != nil {
		t.Errorf("expected nil for missing cache, got %v", loaded)
	}
}

func TestLoadCache_Stale(t *testing.T) {
	home := t.TempDir()
	cachePath := CachePath(home)
	os.MkdirAll(filepath.Dir(cachePath), 0o755)
	// Write a stale cache (25 hours old).
	cache := Cache{
		FetchedAt: time.Now().Add(-25 * time.Hour),
		Entries:   []PriceEntry{{ModelID: "old-model"}},
	}
	data, _ := json.Marshal(cache)
	os.WriteFile(cachePath, data, 0o644)
	loaded := LoadCache(home)
	if loaded != nil {
		t.Errorf("expected nil for stale cache, got %v", loaded)
	}
}

func TestSaveCache_CreatesDir(t *testing.T) {
	home := t.TempDir()
	// Missing parent dirs — SaveCache should create them.
	entries := []PriceEntry{{ModelID: "test"}}
	if err := SaveCache(home, entries); err != nil {
		t.Fatal(err)
	}
	path := CachePath(home)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("cache file should exist")
	}
}

// ── FetchAll with mock HTTP server ──

func TestFetchAll_MockSources(t *testing.T) {
	// Create mock HTTP servers.
	ptServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{"model": "gpt-4o", "input_price": 2.5, "output_price": 10.0},
			{"model": "gemini-2.0-flash", "input_price": 0.1, "output_price": 0.4},
		})
	}))
	defer ptServer.Close()

	orServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// gpt-4o same ID as PT source — tests dedup (first source wins).
		// deepseek is unique to OR.
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{"model": "gpt-4o", "input_price": 2.5, "output_price": 10.0},
			{"model": "deepseek/deepseek-r1", "input_price": 0.14, "output_price": 0.28},
		})
	}))
	defer orServer.Close()

	fetcher := &Fetcher{
		home:   t.TempDir(),
		client: ptServer.Client(),
		sources: []Source{
			&mockSource{name: "mock-pt", base: ptServer.URL},
			&mockSource{name: "mock-or", base: orServer.URL},
		},
	}

	entries := fetcher.FetchAll()
	// Should have: gpt-4o (from PT first), gemini, deepseek = 3 unique
	if len(entries) < 3 {
		t.Errorf("expected >= 3 entries, got %d", len(entries))
	}
	// gpt-4o should come from pricepertoken (first source).
	for _, e := range entries {
		if e.ModelID == "gpt-4o" && e.Source != "mock-pt" {
			t.Errorf("gpt-4o source = %q, want mock-pt (first source wins)", e.Source)
		}
	}
}

func TestFetchAll_AllSourcesFail(t *testing.T) {
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()

	fetcher := &Fetcher{
		home:   t.TempDir(),
		client: failServer.Client(),
		sources: []Source{
			&mockSource{name: "fail", base: failServer.URL},
		},
	}
	entries := fetcher.FetchAll()
	if len(entries) != 0 {
		t.Errorf("expected 0 entries when all sources fail, got %d", len(entries))
	}
}

func TestFetchAll_NoSources(t *testing.T) {
	fetcher := NewFetcher(t.TempDir())
	fetcher.sources = nil
	entries := fetcher.FetchAll()
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

// ── FetchAndUpdate merge tests ──

func TestFetchAndUpdate_MergesIntoCatalog(t *testing.T) {
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()

	fetcher := &Fetcher{
		home:   t.TempDir(),
		client: failServer.Client(),
		sources: []Source{
			&mockSource{name: "fail", base: failServer.URL},
		},
	}

	existing := []llm.ModelInfo{
		{ID: "gpt-4o", InputCost: 5.0, OutputCost: 15.0, Source: llm.SourceSeed},
	}
	// When fetch fails, existing catalog should be returned unchanged.
	result := fetcher.FetchAndUpdate(existing)
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}
	if result[0].ID != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", result[0].ID)
	}
}

func TestFetchAndUpdate_PreservesUserOverrides(t *testing.T) {
	ptServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{"model": "gpt-4o", "input_price": 2.5, "output_price": 10.0},
		})
	}))
	defer ptServer.Close()

	home := t.TempDir()
	fetcher := &Fetcher{
		home:   home,
		client: ptServer.Client(),
		sources: []Source{
			&mockSource{name: "mock-pt", base: ptServer.URL},
		},
	}

	// Existing has a SourceUser entry — should NOT be overwritten.
	existing := []llm.ModelInfo{
		{ID: "gpt-4o", InputCost: 99.0, OutputCost: 99.0, Source: llm.SourceUser},
	}
	result := fetcher.FetchAndUpdate(existing)
	// SourceUser (priority 7) > SourceExternal (priority 6), so user price should stay.
	for _, m := range result {
		if m.ID == "gpt-4o" {
			if m.InputCost != 99.0 {
				t.Errorf("user override overwritten: input = %v, want 99.0", m.InputCost)
			}
			if m.Source != llm.SourceUser {
				t.Errorf("source = %v, want SourceUser", m.Source)
			}
		}
	}
}

// F28: pushRatesToCredits correctly converts per-1M to per-1k.
func TestPushRatesToCredits(t *testing.T) {
	defer credits.SetFetchedRates(nil) // cleanup
	defer credits.SetProviderRates(nil)

	entries := []PriceEntry{
		{ModelID: "gpt-4o", InputPer1M: 2.5, CachedInputPer1M: 1.25, OutputPer1M: 10.0},
		{ModelID: "claude-3.5-sonnet", InputPer1M: 3.0, OutputPer1M: 15.0},
	}
	pushRatesToCredits(entries)
	got := credits.GetFetchedRates()
	if got == nil {
		t.Fatal("expected non-nil fetched rates")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if !closePrice(got["gpt-4o"].InputPer1k, 0.0025) {
		t.Errorf("gpt-4o input = %v, want 0.0025", got["gpt-4o"].InputPer1k)
	}
	if !closePrice(got["gpt-4o"].CachedInputPer1k, 0.00125) {
		t.Errorf("gpt-4o cached input = %v, want 0.00125", got["gpt-4o"].CachedInputPer1k)
	}
	if !closePrice(got["gpt-4o"].OutputPer1k, 0.01) {
		t.Errorf("gpt-4o output = %v, want 0.01", got["gpt-4o"].OutputPer1k)
	}
	// CostFor should use fetched rates.
	cost := credits.CostFor("gpt-4o", 1000, 1000)
	if cost != 0.0125 {
		t.Errorf("CostFor(gpt-4o, 1k, 1k) = %v, want 0.0125", cost)
	}
}

func TestPushRatesToCredits_OpenRouterProviderRates(t *testing.T) {
	defer credits.SetFetchedRates(nil)
	defer credits.SetProviderRates(nil)

	pushRatesToCredits([]PriceEntry{{
		ModelID:          "deepseek/deepseek-chat",
		InputPer1M:       0.14,
		CachedInputPer1M: 0.0028,
		OutputPer1M:      0.28,
		Source:           "openrouter",
		FetchedAt:        time.Now().UTC(),
	}})

	r, key := credits.RateForProvider("openrouter", "deepseek/deepseek-chat")
	if !closePrice(r.InputPer1k, 0.00014) || !closePrice(r.CachedInputPer1k, 0.0000028) || !closePrice(r.OutputPer1k, 0.00028) {
		t.Fatalf("rate=%+v, want OpenRouter provider-specific rate", r)
	}
	if key != "openrouter/deepseek/deepseek-chat (endpoint)" {
		t.Fatalf("key=%q", key)
	}
	if got := credits.CostForProvider("openrouter", "deepseek/deepseek-chat", 1000, 1000); got != 0.00042 {
		t.Fatalf("cost=%v, want 0.00042", got)
	}
}

// ── Mock source for testing ──

type mockSource struct {
	name string
	base string
}

type staticSource struct {
	name    string
	entries []PriceEntry
}

func (s staticSource) Name() string { return s.name }

func (s staticSource) Fetch(client *http.Client) ([]PriceEntry, error) {
	return append([]PriceEntry(nil), s.entries...), nil
}

func (m *mockSource) Name() string { return m.name }

func (m *mockSource) Fetch(client *http.Client) ([]PriceEntry, error) {
	resp, err := client.Get(m.base + "/api/models")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw []struct {
		Model       string  `json:"model"`
		InputPrice  float64 `json:"input_price"`
		OutputPrice float64 `json:"output_price"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	entries := make([]PriceEntry, 0, len(raw))
	for _, r := range raw {
		if r.Model == "" {
			continue
		}
		entries = append(entries, PriceEntry{
			ModelID:     r.Model,
			InputPer1M:  r.InputPrice,
			OutputPer1M: r.OutputPrice,
			Source:      m.name,
			FetchedAt:   now,
		})
	}
	return entries, nil
}
