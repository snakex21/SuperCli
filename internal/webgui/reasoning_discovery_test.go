package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func localReasoningServer(t *testing.T, baseURL, model string) *Server {
	t.Helper()
	s := newTestServer(t, false)
	s.eng.cfg.Provider, s.eng.cfg.BaseURL, s.eng.cfg.Model = config.ProviderOpenAI, baseURL, model
	s.eng.caps.Register(llm.HeuristicCapabilities(model))
	p, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: baseURL, Model: model, Capabilities: s.eng.caps})
	if err != nil {
		t.Fatal(err)
	}
	s.eng.prov = p
	err = config.SaveToml(filepath.Join(s.eng.dataDir, "config.toml"), config.TomlConfig{
		Providers: []config.ProviderConf{{Name: "local", Type: config.ProviderOpenAI, BaseURL: baseURL, Model: model, CachedModels: []string{model}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cachedModelCount(s.eng.providerManager(), s.eng.caps) == 0 {
		t.Fatal("fixture must reproduce a saved inventory without native capabilities")
	}
	return s
}

func TestReasoningDiscoversLocalToggleWithCachedInventory(t *testing.T) {
	t.Cleanup(func() { _ = llm.SetReasoningEffort("") })
	_ = llm.SetReasoningEffort("xhigh")
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/api/v1/models" {
			t.Errorf("unexpected discovery or inference request: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"models\":[{\"key\":\"qwen-local\",\"architecture\":\"qwen35\",\"capabilities\":{\"reasoning\":{\"allowed_options\":[\"off\",\"on\"],\"default\":\"on\"}}}]}"))
	}))
	defer upstream.Close()
	s := localReasoningServer(t, upstream.URL+"/v1", "qwen-local")

	// Both endpoints are requested during GUI startup. Cached names must not
	// skip native metadata, and concurrent refreshes must share one discovery.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(models bool) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/reasoning", nil)
			var view reasoningView
			if models {
				s.handleModels(rec, req)
				var result modelsResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Error(err)
				}
				view = result.Reasoning
			} else {
				s.handleReasoning(rec, req)
				if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
					t.Error(err)
				}
			}
			if rec.Code != 200 || !view.ToggleOnly || view.Effective != "on" || view.Selected != "high" || strings.Join(view.Levels, ",") != "none,high" {
				t.Errorf("status=%d view=%+v", rec.Code, view)
			}
		}(i%2 == 0)
	}
	wg.Wait()
	if requests.Load() != 1 {
		t.Fatalf("native metadata requests=%d, want one per endpoint per app run", requests.Load())
	}
}

func TestReasoningDiscoveryUnavailableDoesNotRepeatOnRefresh(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	s := localReasoningServer(t, upstream.URL+"/v1", "generic-local")
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		s.handleReasoning(rec, httptest.NewRequest(http.MethodGet, "/api/reasoning", nil))
		if rec.Code != 200 {
			t.Fatal(rec.Code)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("requests=%d, want one v1/v0 discovery attempt, no refresh retry loop", requests.Load())
	}
}

// Opt-in metadata-only check: no chat completions or model loading.
func TestReasoningLocalDiscoveryLive(t *testing.T) {
	baseURL, model := os.Getenv("SUPERCLI_REASONING_DISCOVERY_URL"), os.Getenv("SUPERCLI_REASONING_DISCOVERY_MODEL")
	if baseURL == "" || model == "" {
		t.Skip("set SUPERCLI_REASONING_DISCOVERY_URL and SUPERCLI_REASONING_DISCOVERY_MODEL")
	}
	if !llm.IsLocalBaseURL(baseURL) {
		t.Fatal("metadata check must use a local endpoint")
	}
	t.Cleanup(func() { _ = llm.SetReasoningEffort("") })
	_ = llm.SetReasoningEffort("xhigh")
	s := localReasoningServer(t, baseURL, model)
	rec := httptest.NewRecorder()
	s.handleReasoning(rec, httptest.NewRequest(http.MethodGet, "/api/reasoning", nil))
	var view reasoningView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if !view.ToggleOnly || view.Effective != "on" || strings.Join(view.Levels, ",") != "none,high" {
		t.Fatalf("local GUI response=%+v", view)
	}
	t.Logf("GUI response: toggle=%v, configured=%s, effective=%s, levels=%v", view.ToggleOnly, view.Configured, view.Effective, view.Levels)
}
