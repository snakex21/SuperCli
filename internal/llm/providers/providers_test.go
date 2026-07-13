package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func TestNewManager(t *testing.T) {
	m := NewManager("/tmp/test-home")
	if m.home != "/tmp/test-home" {
		t.Fatalf("home = %q", m.home)
	}
	if m.hidden == nil {
		t.Fatal("hidden map should be initialized")
	}
}

func TestToggleHidden(t *testing.T) {
	m := NewManager("/tmp/x")
	if m.IsHidden("gpt-4") {
		t.Fatal("should not be hidden initially")
	}
	hidden := m.ToggleHidden("gpt-4")
	if !hidden {
		t.Fatal("first toggle should hide")
	}
	if !m.IsHidden("gpt-4") {
		t.Fatal("should be hidden after toggle")
	}
	hidden = m.ToggleHidden("gpt-4")
	if hidden {
		t.Fatal("second toggle should unhide")
	}
	if m.IsHidden("gpt-4") {
		t.Fatal("should not be hidden after second toggle")
	}
}

func TestAddAndRemove(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)

	err := m.Add("my-openai", "openai", "https://api.openai.com/v1", "sk-test", "gpt-4")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Verify it's there.
	m.Reload()
	if len(m.providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(m.providers))
	}
	if m.providers[0].Name != "my-openai" {
		t.Fatalf("provider name = %q", m.providers[0].Name)
	}

	// Duplicate should fail.
	err = m.Add("my-openai", "openai", "https://api.openai.com/v1", "sk-test", "gpt-4")
	if err == nil {
		t.Fatal("duplicate add should fail")
	}

	// Remove.
	err = m.Remove("my-openai")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	m.Reload()
	if len(m.providers) != 0 {
		t.Fatalf("providers = %d, want 0", len(m.providers))
	}

	// Remove nonexistent should fail.
	err = m.Remove("no-such")
	if err == nil {
		t.Fatal("remove nonexistent should fail")
	}
}

func TestUpdate(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)

	m.Add("p1", "openai", "http://old", "", "gpt-4")
	base := "http://new"
	err := m.Update("p1", nil, &base, nil, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	m.Reload()
	if m.providers[0].BaseURL != "http://new" {
		t.Fatalf("base_url = %q", m.providers[0].BaseURL)
	}

	// Update nonexistent.
	err = m.Update("no-such", nil, nil, nil, nil)
	if err == nil {
		t.Fatal("update nonexistent should fail")
	}
}

func TestSetDisabledPreservesProviderAndExcludesModels(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)
	if err := m.Add("sleeping-local", "openai", "http://other-pc:8080/v1", "secret", "local-moe"); err != nil {
		t.Fatal(err)
	}
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "local-moe", Provider: "sleeping-local", Source: llm.SourceProbe})
	if err := m.SetDisabled("sleeping-local", true); err != nil {
		t.Fatal(err)
	}
	m.Reload()
	configured := m.ListConfigured(caps)
	if len(configured) != 1 || !configured[0].Disabled || configured[0].HasKey != true {
		t.Fatalf("disabled provider was not preserved: %+v", configured)
	}
	if len(configured[0].Models) != 0 {
		t.Fatalf("disabled provider leaked models: %+v", configured[0].Models)
	}
	if res := m.ScanProvider("sleeping-local", caps); res.Err == nil || !strings.Contains(res.Err.Error(), "disabled") {
		t.Fatalf("disabled scan = %v, want disabled error", res.Err)
	}
	if err := m.SetDisabled("sleeping-local", false); err != nil {
		t.Fatal(err)
	}
	m.Reload()
	if m.Configured()[0].Disabled {
		t.Fatal("provider did not re-enable")
	}
}

func TestSetPrice(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)

	err := m.SetPrice("gpt-4", 30.0, 60.0)
	if err != nil {
		t.Fatalf("SetPrice: %v", err)
	}

	tc, err := config.LoadToml(m.tomlPath)
	if err != nil {
		t.Fatalf("LoadToml: %v", err)
	}
	if len(tc.ModelPrices) != 1 {
		t.Fatalf("model_prices = %d, want 1", len(tc.ModelPrices))
	}
	if tc.ModelPrices[0].InputCost != 30.0 {
		t.Fatalf("input_cost = %f, want 30.0", tc.ModelPrices[0].InputCost)
	}

	// Update same model.
	err = m.SetPrice("gpt-4", 25.0, 50.0)
	if err != nil {
		t.Fatalf("SetPrice update: %v", err)
	}
	tc, _ = config.LoadToml(m.tomlPath)
	if len(tc.ModelPrices) != 1 {
		t.Fatalf("model_prices = %d after update, want 1", len(tc.ModelPrices))
	}
	if tc.ModelPrices[0].InputCost != 25.0 {
		t.Fatalf("input_cost = %f after update, want 25.0", tc.ModelPrices[0].InputCost)
	}
}

func TestSetProviderPriceScopesSameModelAndRemovesExactly(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.SetProviderPrice("direct", "gpt-same", 5, 0.5, 30); err != nil {
		t.Fatal(err)
	}
	if err := m.SetProviderPrice("router", "gpt-same", 7, 0.7, 40); err != nil {
		t.Fatal(err)
	}
	tc, err := config.LoadToml(m.tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(tc.ModelPrices) != 2 || tc.ModelPrices[0].Provider != "direct" || tc.ModelPrices[1].Provider != "router" {
		t.Fatalf("provider-scoped prices were merged unexpectedly: %+v", tc.ModelPrices)
	}
	if tc.ModelPrices[0].CachedInputCost != 0.5 || tc.ModelPrices[1].OutputCost != 40 {
		t.Fatalf("price fields were not preserved: %+v", tc.ModelPrices)
	}
	if err := m.RemoveProviderPrice("direct", "gpt-same"); err != nil {
		t.Fatal(err)
	}
	tc, _ = config.LoadToml(m.tomlPath)
	if len(tc.ModelPrices) != 1 || tc.ModelPrices[0].Provider != "router" {
		t.Fatalf("exact remove affected the wrong price: %+v", tc.ModelPrices)
	}
	if err := m.SetProviderPrice("router", "bad", -1, 0, 0); err == nil {
		t.Fatal("negative price should be rejected")
	}
}

func TestProviderInfo(t *testing.T) {
	pi := ProviderInfo{
		Name:      "test",
		Type:      "openai",
		BaseURL:   "http://localhost",
		Connected: true,
	}
	if pi.Name != "test" {
		t.Fatalf("Name = %q", pi.Name)
	}
	if !pi.Connected {
		t.Fatal("should be connected")
	}
}

func TestPublicGatewayPlaceholderIsNotStoredOrReportedAsUserKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
	}{
		{name: "kilo", url: "https://api.kilo.ai/api/openrouter"},
		{name: "opencode", url: "https://opencode.ai/zen/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(t.TempDir())
			if err := m.Add(tc.name, "openai", tc.url, "", ""); err != nil {
				t.Fatal(err)
			}
			m.Reload()
			if got := m.Configured()[0].APIKey; got != "" {
				t.Fatalf("stored synthetic key %q", got)
			}
			info := m.ListConfigured(nil)[0]
			if info.HasKey {
				t.Fatal("public access was reported as a configured key")
			}
			if key, ok := m.APIKey(tc.name); !ok || key != "" {
				t.Fatalf("APIKey = %q, %v; want empty, true", key, ok)
			}
		})
	}
}

func TestSaveActiveConfig(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)

	// Initially empty.
	if got := m.LoadActiveModel(); got != "" {
		t.Fatalf("LoadActiveModel = %q, want empty", got)
	}

	// Save a model with provider.
	if err := m.SaveActiveConfig("deepseek-r1", "deepseek"); err != nil {
		t.Fatalf("SaveActiveConfig: %v", err)
	}

	// Load it back.
	if got := m.LoadActiveModel(); got != "deepseek-r1" {
		t.Fatalf("LoadActiveModel = %q, want %q", got, "deepseek-r1")
	}

	// Overwrite with different model.
	if err := m.SaveActiveConfig("gpt-4o", "openai"); err != nil {
		t.Fatalf("SaveActiveConfig overwrite: %v", err)
	}
	if got := m.LoadActiveModel(); got != "gpt-4o" {
		t.Fatalf("LoadActiveModel = %q, want %q", got, "gpt-4o")
	}
}

func TestSaveActiveConfigPreservesProviders(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)

	// Add a provider first.
	m.Add("p1", "openai", "http://localhost", "sk-test", "gpt-4")

	// Save active config — should not clobber providers.
	if err := m.SaveActiveConfig("claude-3", "p1"); err != nil {
		t.Fatalf("SaveActiveConfig: %v", err)
	}

	// Reload and verify providers still there.
	m.Reload()
	if len(m.providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(m.providers))
	}
	if m.providers[0].Name != "p1" {
		t.Fatalf("provider name = %q, want p1", m.providers[0].Name)
	}

	// Verify default_provider is set in config.toml.
	tc, err := config.LoadToml(m.tomlPath)
	if err != nil {
		t.Fatalf("LoadToml: %v", err)
	}
	if tc.DefaultProvider != "p1" {
		t.Fatalf("default_provider = %q, want %q", tc.DefaultProvider, "p1")
	}
}

// TestSaveActiveConfig_ProjectPathSurvivesStartup is a regression
// test for the "selected model not remembered between sessions" bug.
// The Manager persists the runtime /model selection to the GLOBAL
// config, but startup resolution merges a project config
// (<cwd>/.supercli/config.toml) with HIGHER priority. Without
// SetActiveConfigPath, the project config silently shadows the saved
// selection and the model swap is forgotten on the next launch.
func TestSaveActiveConfig_ProjectPathSurvivesStartup(t *testing.T) {
	dataDir := t.TempDir()
	cwd := t.TempDir()

	// A project config pins a different model.
	projDir := filepath.Join(cwd, ".supercli")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "config.toml"),
		[]byte("default_model = \"deepseek-v4-flash\"\ndefault_provider = \"deepseek\"\n"),
		0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(dataDir)
	// main.go wiring: persist the active selection to the
	// highest-priority config in effect (the project config).
	if _, projectPath := config.FindTomlPaths(dataDir, cwd); projectPath != "" {
		m.SetActiveConfigPath(projectPath)
	}
	if err := m.SaveActiveConfig("gpt-5.5", "codex"); err != nil {
		t.Fatalf("SaveActiveConfig: %v", err)
	}

	// Startup resolution: global < project. The saved model must win.
	tc, err := config.ResolveConfig(dataDir, cwd, "")
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if tc.DefaultModel != "gpt-5.5" {
		t.Fatalf("after restart resolution default_model = %q, want gpt-5.5", tc.DefaultModel)
	}
	if tc.DefaultProvider != "codex" {
		t.Fatalf("after restart resolution default_provider = %q, want codex", tc.DefaultProvider)
	}

	// The global config must also reflect the choice (mirror), so a
	// later launch without the project config still remembers it.
	gtc, err := config.LoadToml(filepath.Join(dataDir, "config.toml"))
	if err != nil {
		t.Fatalf("LoadToml global: %v", err)
	}
	if gtc.DefaultModel != "gpt-5.5" {
		t.Fatalf("global default_model = %q, want gpt-5.5 (mirror)", gtc.DefaultModel)
	}
}

// ---------- Names -----------------------------------------------------------

func TestNames(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)
	m.Add("lmstudio", "openai", "http://localhost:1234/v1", "", "")
	m.Add("openai", "openai", "https://api.openai.com/v1", "sk-test", "")

	names := m.Names()
	if len(names) != 2 {
		t.Fatalf("Names = %d, want 2", len(names))
	}
	if names[0] != "lmstudio" || names[1] != "openai" {
		t.Fatalf("Names = %v, want [lmstudio openai]", names)
	}
}

func TestNames_Empty(t *testing.T) {
	m := NewManager(t.TempDir())
	names := m.Names()
	if len(names) != 0 {
		t.Fatalf("Names = %d, want 0", len(names))
	}
}

// ---------- LoadHiddenState -------------------------------------------------

func TestLoadHiddenState(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)

	// Pre-populate config.toml with hidden models.
	tc := config.TomlConfig{
		HiddenModels: []string{"gpt-4", "claude-3"},
	}
	if err := config.SaveToml(m.tomlPath, tc); err != nil {
		t.Fatalf("SaveToml: %v", err)
	}

	m.LoadHiddenState()

	if !m.IsHidden("gpt-4") {
		t.Fatal("gpt-4 should be hidden")
	}
	if !m.IsHidden("claude-3") {
		t.Fatal("claude-3 should be hidden")
	}
	if m.IsHidden("gemma") {
		t.Fatal("gemma should not be hidden")
	}
}

func TestLoadHiddenState_Empty(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)
	m.LoadHiddenState() // no config file, should not panic

	if m.IsHidden("anything") {
		t.Fatal("nothing should be hidden")
	}
}

// ---------- ShowModel / HideModel persistence -------------------------------

func TestShowModelPersists(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)

	// Hide a model.
	m.HideModel("qwen")
	if !m.IsHidden("qwen") {
		t.Fatal("qwen should be hidden after HideModel")
	}

	// Show it.
	changed := m.ShowModel("qwen")
	if !changed {
		t.Fatal("ShowModel should return true when unhiding")
	}
	if m.IsHidden("qwen") {
		t.Fatal("qwen should be visible after ShowModel")
	}

	// Verify config.toml was updated.
	tc, _ := config.LoadToml(m.tomlPath)
	if len(tc.HiddenModels) != 0 {
		t.Fatalf("HiddenModels = %v, want empty", tc.HiddenModels)
	}
}

func TestHideModelPersists(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)

	m.HideModel("gpt-4")
	m.HideModel("claude-3")

	// Verify config.toml has both.
	tc, err := config.LoadToml(m.tomlPath)
	if err != nil {
		t.Fatalf("LoadToml: %v", err)
	}
	if len(tc.HiddenModels) != 2 {
		t.Fatalf("HiddenModels = %d, want 2", len(tc.HiddenModels))
	}

	// Reload manager from disk and verify state survives.
	m2 := NewManager(home)
	m2.LoadHiddenState()
	if !m2.IsHidden("gpt-4") {
		t.Fatal("gpt-4 should be hidden after reload")
	}
	if !m2.IsHidden("claude-3") {
		t.Fatal("claude-3 should be hidden after reload")
	}
}

func TestHideModel_AlreadyHidden(t *testing.T) {
	m := NewManager(t.TempDir())
	m.HideModel("x")
	changed := m.HideModel("x") // second time
	if changed {
		t.Fatal("second HideModel should return false")
	}
}

func TestShowModel_AlreadyVisible(t *testing.T) {
	m := NewManager(t.TempDir())
	changed := m.ShowModel("x")
	if changed {
		t.Fatal("ShowModel on visible model should return false")
	}
}

// ---------- probeProvider ---------------------------------------------------

func TestProbeProvider_Success(t *testing.T) {
	// Start a fake /v1/models endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" && r.Method == "GET" {
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{}})
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	ok, err := probeProvider(config.ProviderConf{
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("probeProvider: %v", err)
	}
	if !ok {
		t.Fatal("expected connected")
	}
}

func TestProbeProvider_BaseURL_HasV1Suffix(t *testing.T) {
	// The key fix: BaseURL already ends with /v1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" && r.Method == "GET" {
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{}})
			return
		}
		// /v1/v1/models should never be hit.
		if strings.Contains(r.URL.Path, "/v1/v1") {
			t.Errorf("double /v1 detected: %s", r.URL.Path)
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	// BaseURL includes /v1 — probe should NOT produce /v1/v1/models.
	ok, err := probeProvider(config.ProviderConf{
		BaseURL: srv.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("probeProvider with /v1 suffix: %v", err)
	}
	if !ok {
		t.Fatal("expected connected when BaseURL has /v1 suffix")
	}
}

func TestProbeProvider_BaseURL_NoV1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" && r.Method == "GET" {
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{}})
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	// BaseURL does NOT include /v1 — probe should append /v1/models.
	ok, err := probeProvider(config.ProviderConf{
		BaseURL: srv.URL, // no /v1
	})
	if err != nil {
		t.Fatalf("probeProvider without /v1: %v", err)
	}
	if !ok {
		t.Fatal("expected connected when BaseURL has no /v1")
	}
}

func TestProbeProvider_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	ok, err := probeProvider(config.ProviderConf{BaseURL: srv.URL})
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if ok {
		t.Fatal("expected not connected")
	}
}

func TestProbeProvider_EmptyBaseURL(t *testing.T) {
	ok, err := probeProvider(config.ProviderConf{BaseURL: ""})
	if err == nil {
		t.Fatal("expected error for empty base URL")
	}
	if ok {
		t.Fatal("expected not connected")
	}
}

func TestProbeProvider_Timeout(t *testing.T) {
	// Use a non-routable IP that will cause a connection timeout.
	// 192.0.2.0/24 is reserved for documentation (TEST-NET-1).
	ok, err := probeProvider(config.ProviderConf{
		BaseURL: "http://192.0.2.1:9999/v1",
	})
	if err == nil {
		t.Fatal("expected timeout error for unreachable IP")
	}
	if ok {
		t.Fatal("expected not connected on timeout")
	}
}

// ---------- ScanModels with HTTP mock ---------------------------------------

func TestScanModels_FetchesModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" && r.Method == "GET" {
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "qwen3.6-35b-a3b"},
					{"id": "gemma-4-26b"},
				},
			})
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	home := t.TempDir()
	m := NewManager(home)
	m.Add("test-prov", "openai", srv.URL+"/v1", "", "")
	m.Reload()

	caps := llm.NewCapabilityRegistry()
	count := m.ScanModels(caps)
	if count != 2 {
		t.Fatalf("ScanModels = %d, want 2", count)
	}

	mi, ok := caps.Get("qwen3.6-35b-a3b")
	if !ok {
		t.Fatal("qwen3.6-35b-a3b not found in registry")
	}
	if mi.Provider != "test-prov" {
		t.Fatalf("Provider = %q, want test-prov", mi.Provider)
	}
}

func TestScanModels_AppendsV1WhenBaseURLHasNoSuffix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "deepseek-chat"}},
		})
	}))
	defer srv.Close()

	m := NewManager(t.TempDir())
	m.Add("deepseek", "openai", srv.URL, "", "")
	m.Reload()
	caps := llm.NewCapabilityRegistry()

	if got := m.ScanModels(caps); got != 1 {
		t.Fatalf("ScanModels = %d, want 1", got)
	}
	mi, ok := caps.Get("deepseek-chat")
	if !ok || mi.Provider != "deepseek" || mi.Source != llm.SourceProvider {
		t.Fatalf("model = %+v ok=%v", mi, ok)
	}
}

func TestScanModels_RequiresValidAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer valid-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "deepseek-reasoner"}},
		})
	}))
	defer srv.Close()

	m := NewManager(t.TempDir())
	m.Add("deepseek", "openai", srv.URL+"/v1", "bad-key", "")
	m.Reload()
	caps := llm.NewCapabilityRegistry()
	if got := m.ScanModels(caps); got != 0 {
		t.Fatalf("ScanModels with bad key = %d, want 0", got)
	}
	if _, ok := caps.Get("deepseek-reasoner"); ok {
		t.Fatal("model should not be registered when key is invalid")
	}

	key := "valid-key"
	if err := m.Update("deepseek", nil, nil, &key, nil); err != nil {
		t.Fatalf("Update key: %v", err)
	}
	m.Reload()
	if got := m.ScanModels(caps); got != 1 {
		t.Fatalf("ScanModels with valid key = %d, want 1", got)
	}
}

func TestScanProvider_ReturnsErrorForInvalidKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer valid-key" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "deepseek-chat"}}})
	}))
	defer srv.Close()

	m := NewManager(t.TempDir())
	m.Add("deepseek", "openai", srv.URL+"/v1", "bad-key", "")
	m.Reload()
	caps := llm.NewCapabilityRegistry()

	res := m.ScanProvider("deepseek", caps)
	if res.Err == nil {
		t.Fatal("ScanProvider error = nil, want invalid key error")
	}
	if !strings.Contains(res.Err.Error(), "401") {
		t.Fatalf("ScanProvider error = %q, want 401", res.Err.Error())
	}
	if len(res.Models) != 0 {
		t.Fatalf("models = %v, want none", res.Models)
	}
	if _, ok := caps.Get("deepseek-chat"); ok {
		t.Fatal("model registered despite invalid key")
	}
}

func TestScanProvider_RegistersOnlyAPIReturnedModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer valid-key" {
			t.Fatalf("auth = %q, want Bearer valid-key", got)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "deepseek-chat"},
				{"id": "deepseek-reasoner"},
			},
		})
	}))
	defer srv.Close()

	m := NewManager(t.TempDir())
	m.Add("deepseek", "openai", srv.URL+"/v1", "valid-key", "")
	m.Reload()
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "deepseek-r1", Provider: "deepseek", Source: llm.SourceSeed})

	res := m.ScanProvider("deepseek", caps)
	if res.Err != nil {
		t.Fatalf("ScanProvider: %v", res.Err)
	}
	if len(res.Models) != 2 {
		t.Fatalf("models = %v, want 2", res.Models)
	}
	if _, ok := caps.Get("deepseek-chat"); !ok {
		t.Fatal("deepseek-chat not registered")
	}
	if _, ok := caps.Get("deepseek-reasoner"); !ok {
		t.Fatal("deepseek-reasoner not registered")
	}
	rows := m.List(caps)
	if len(rows) != 1 || len(rows[0].Models) != 2 {
		t.Fatalf("provider rows = %+v, want exactly API-returned models", rows)
	}
}

func TestScanProvider_OpenCodeKiloShowOnlyExplicitFreeModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "claude-sonnet-4-5"},
				{"id": "gpt-5.4-mini"},
				{"id": "deepseek-v4-flash-free"},
				{"id": "openai/gpt-oss-20b:free"},
				{"id": "kilo-auto/free"},
			},
		})
	}))
	defer srv.Close()

	m := NewManager(t.TempDir())
	m.Add("opencode", "openai", srv.URL+"/v1", "", "")
	m.Add("kilo", "openai", srv.URL+"/v1", "", "")
	m.Reload()
	caps := llm.NewCapabilityRegistry()

	for _, name := range []string{"opencode", "kilo"} {
		res := m.ScanProvider(name, caps)
		if res.Err != nil {
			t.Fatalf("ScanProvider(%s): %v", name, res.Err)
		}
		if len(res.Models) != 3 {
			t.Fatalf("ScanProvider(%s) models = %v, want 3 explicit free models", name, res.Models)
		}
		for _, id := range res.Models {
			if !llm.IsFreeModelID(id) {
				t.Fatalf("ScanProvider(%s) returned non-free model %q", name, id)
			}
		}
	}
	if _, ok := caps.Get("claude-sonnet-4-5"); ok {
		t.Fatal("paid opencode model should not be registered")
	}
}

func TestListConfigured_OpenCodeHidesCachedPaidModels(t *testing.T) {
	m := NewManager(t.TempDir())
	m.Add("opencode", "openai", "https://opencode.ai/zen/v1", "", "")
	m.Reload()
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "gpt-5.4-mini", Provider: "opencode", Source: llm.SourceProvider})
	caps.Register(llm.ModelInfo{ID: "deepseek-v4-flash-free", Provider: "opencode", Source: llm.SourceProvider})

	rows := m.ListConfigured(caps)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if len(rows[0].Models) != 1 || rows[0].Models[0].ID != "deepseek-v4-flash-free" {
		t.Fatalf("models = %+v, want only explicit free model", rows[0].Models)
	}
}

func TestSetModelsHiddenPersistsBatchOnceAndDeduplicates(t *testing.T) {
	m := NewManager(t.TempDir())
	if changed := m.SetModelsHidden([]string{"a", "b", "a", "  "}, true); changed != 2 {
		t.Fatalf("hide changed = %d, want 2", changed)
	}
	if changed := m.SetModelsHidden([]string{"a", "b"}, true); changed != 0 {
		t.Fatalf("second hide changed = %d, want 0", changed)
	}

	reloaded := NewManager(m.home)
	reloaded.LoadHiddenState()
	if !reloaded.IsHidden("a") || !reloaded.IsHidden("b") {
		t.Fatal("batch-hidden models were not persisted")
	}
	if changed := reloaded.SetModelsHidden([]string{"a", "b"}, false); changed != 2 {
		t.Fatalf("show changed = %d, want 2", changed)
	}
}

func TestReloadRepairsLegacyUnnamedProviderWithoutLosingConfiguration(t *testing.T) {
	m := NewManager(t.TempDir())
	m.providers = []config.ProviderConf{{
		Name: "", Type: "openai", BaseURL: "https://newapi.example.test/v1",
		APIKey: "secret", Model: "model-a",
	}}
	if err := m.saveLocked(); err != nil {
		t.Fatal(err)
	}

	m.Reload()
	if len(m.providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(m.providers))
	}
	got := m.providers[0]
	if got.Name != "newapi.example.test" || got.APIKey != "secret" || got.Model != "model-a" {
		t.Fatalf("repaired provider = %+v", got)
	}

	reloaded := NewManager(m.home)
	reloaded.Reload()
	if len(reloaded.providers) != 1 || reloaded.providers[0].Name != "newapi.example.test" {
		t.Fatalf("repair was not persisted: %+v", reloaded.providers)
	}
}

func TestAddRejectsEmptyProviderName(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.Add("  ", "openai", "https://example.test/v1", "", ""); err == nil {
		t.Fatal("expected empty provider name to be rejected")
	}
}

func TestAddAndUpdateCleanPastedAPIKey(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.Add("deepseek", "openai", "https://api.deepseek.com/v1", " sk-\r\ntest\t ", ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	m.Reload()
	if len(m.providers) != 1 || m.providers[0].APIKey != "sk-test" {
		t.Fatalf("api key after add = %q, want sk-test", m.providers[0].APIKey)
	}
	key := " sk-2\n "
	if err := m.Update("deepseek", nil, nil, &key, nil); err != nil {
		t.Fatalf("Update: %v", err)
	}
	m.Reload()
	if m.providers[0].APIKey != "sk-2" {
		t.Fatalf("api key after update = %q, want sk-2", m.providers[0].APIKey)
	}
}

func TestUpdateAPIKeyPresenceSemantics(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.Add("p", "openai", "https://example.test/v1", "sk-secret", "m"); err != nil {
		t.Fatal(err)
	}
	model := "m2"
	if err := m.Update("p", nil, nil, nil, &model); err != nil {
		t.Fatal(err)
	}
	m.Reload()
	if got, ok := m.APIKey("p"); !ok || got != "sk-secret" {
		t.Fatalf("omitted API key = %q, ok=%v; want preserved", got, ok)
	}
	for _, invalid := range []string{"   ", "Bearer ", "``", `""`} {
		if err := m.Update("p", nil, nil, &invalid, nil); err == nil {
			t.Fatalf("API key %q should be rejected after cleaning to empty", invalid)
		}
		if got, ok := m.APIKey("p"); !ok || got != "sk-secret" {
			t.Fatalf("invalid API key %q changed stored key to %q, ok=%v", invalid, got, ok)
		}
	}
	empty := ""
	if err := m.Update("p", nil, nil, &empty, nil); err != nil {
		t.Fatal(err)
	}
	m.Reload()
	if got, ok := m.APIKey("p"); !ok || got != "" {
		t.Fatalf("explicit empty API key = %q, ok=%v; want cleared", got, ok)
	}
}

func TestList_DoesNotExposeSeedModelsForConfiguredProvider(t *testing.T) {
	m := NewManager(t.TempDir())
	m.Add("deepseek", "openai", "http://127.0.0.1:1/v1", "", "")
	m.Reload()

	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "deepseek-r1", Provider: "deepseek", Source: llm.SourceSeed})
	caps.Register(llm.ModelInfo{ID: "deepseek-chat", Provider: "deepseek", Source: llm.SourceProvider})

	rows := m.List(caps)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if len(rows[0].Models) != 1 || rows[0].Models[0].ID != "deepseek-chat" {
		t.Fatalf("models = %+v, want only scanned deepseek-chat", rows[0].Models)
	}
}

func TestScanModels_UnreachableProvider(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)
	m.Add("down", "openai", "http://127.0.0.1:1/v1", "", "")
	m.Reload()

	caps := llm.NewCapabilityRegistry()
	count := m.ScanModels(caps)
	if count != 0 {
		t.Fatalf("ScanModels = %d, want 0 (provider down)", count)
	}
}

func TestScanModels_NoProviders(t *testing.T) {
	m := NewManager(t.TempDir())
	caps := llm.NewCapabilityRegistry()
	count := m.ScanModels(caps)
	if count != 0 {
		t.Fatalf("ScanModels = %d, want 0", count)
	}
}
