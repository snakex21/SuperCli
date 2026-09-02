package config

import "testing"

func TestRuntimeProviderNameDoesNotMislabelWorkerAsDefaultProvider(t *testing.T) {
	tc := TomlConfig{
		DefaultProvider: "main",
		Providers: []ProviderConf{
			{Name: "main", Type: "openai", BaseURL: "https://main.example/v1"},
			{Name: "worker", Type: "openai", BaseURL: "https://worker.example/v1"},
		},
	}
	got := RuntimeProviderName(tc, Config{Provider: "openai", BaseURL: "https://worker.example/v1", Model: "m"})
	if got != "worker" {
		t.Fatalf("RuntimeProviderName = %q, want worker", got)
	}
}

func TestModelContextStoreScopesSameModelByProvider(t *testing.T) {
	dir := t.TempDir()
	store := LoadModelContextStore(dir)
	if err := store.Set("anyrouter", "gpt-5.6-sol", 100_000); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get("openai", "gpt-5.6-sol"); ok {
		t.Fatal("AnyRouter override leaked into OpenAI")
	}
	if got, ok := store.Get("anyrouter", "gpt-5.6-sol"); !ok || got != 100_000 {
		t.Fatalf("AnyRouter override = %d, %v", got, ok)
	}
	reloaded := LoadModelContextStore(dir)
	if got, ok := reloaded.Get("anyrouter", "gpt-5.6-sol"); !ok || got != 100_000 {
		t.Fatalf("reloaded override = %d, %v", got, ok)
	}
	if removed, err := reloaded.Remove("anyrouter", "gpt-5.6-sol"); err != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, err)
	}
}

func TestParseContextBudget(t *testing.T) {
	for input, want := range map[string]int{"100k": 100_000, "128K": 128_000, "131072": 131_072, "1m": 1_000_000} {
		got, auto, err := ParseContextBudget(input)
		if err != nil || auto || got != want {
			t.Errorf("ParseContextBudget(%q) = %d, %v, %v; want %d", input, got, auto, err, want)
		}
	}
	for _, input := range []string{"auto", "0", ""} {
		if got, auto, err := ParseContextBudget(input); err != nil || !auto || got != 0 {
			t.Errorf("ParseContextBudget(%q) = %d, %v, %v", input, got, auto, err)
		}
	}
}
