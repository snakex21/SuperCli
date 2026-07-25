package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// TestReloadPublishesCachedModelsToTransports covers the wiring that makes a
// "model not available" error useful: the inventory in config.toml has to reach
// internal/llm, which only ever sees a base URL.
func TestReloadPublishesCachedModelsToTransports(t *testing.T) {
	llm.RememberProviderModels("https://anyrouter.top/v1", nil)
	t.Cleanup(func() { llm.RememberProviderModels("https://anyrouter.top/v1", nil) })

	home := t.TempDir()
	tomlPath := filepath.Join(home, "config.toml")
	if err := config.SaveToml(tomlPath, config.TomlConfig{Providers: []config.ProviderConf{{
		Name:         "any-router",
		Type:         config.ProviderOpenAI,
		BaseURL:      "https://anyrouter.top/v1",
		CachedModels: []string{"claude-opus-4-7", "gpt-5.6-sol"},
	}}}); err != nil {
		t.Fatal(err)
	}

	if got := llm.ProviderModels("https://anyrouter.top/v1"); got != nil {
		t.Fatalf("catalog should start empty, got %v", got)
	}
	NewManager(home).Reload()
	got := llm.ProviderModels("https://anyrouter.top/v1")
	if len(got) != 2 || got[0] != "claude-opus-4-7" || got[1] != "gpt-5.6-sol" {
		t.Fatalf("cached models not published to the transport layer: %v", got)
	}
}

// TestScanProviderRefreshesTransportCatalog checks a live scan updates the same
// map, so the list a rejected model is compared against is the fresh one.
func TestScanProviderRefreshesTransportCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
			{"id": "fresh-a"}, {"id": "fresh-b"},
		}})
	}))
	defer srv.Close()
	base := srv.URL + "/v1"
	t.Cleanup(func() { llm.RememberProviderModels(base, nil) })

	home := t.TempDir()
	m := NewManager(home)
	if err := m.Add("scanme", config.ProviderOpenAI, base, "key", ""); err != nil {
		t.Fatal(err)
	}
	m.Reload()
	// Pretend the saved inventory is out of date.
	llm.RememberProviderModels(base, []string{"stale-only"})

	if res := m.ScanProvider("scanme", llm.NewCapabilityRegistry()); res.Err != nil {
		t.Fatalf("ScanProvider: %v", res.Err)
	}
	got := llm.ProviderModels(base)
	if len(got) != 2 || got[0] != "fresh-a" || got[1] != "fresh-b" {
		t.Fatalf("scan did not refresh the transport catalog: %v", got)
	}
}
