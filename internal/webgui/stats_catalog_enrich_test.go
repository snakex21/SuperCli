package webgui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"supercli/internal/llm"
)

// Opening the GUI with a saved gateway inventory (no rescan, no request yet)
// must show the catalog window, while explicit overrides still win.
func TestStatsActiveContextUsesEnrichedGatewayCatalogBeforeAnyRequest(t *testing.T) {
	const model = "openai/gpt-gui-fixture"
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"openai/gpt-gui-fixture","context_length":1050000}]}`))
	}))
	defer catalog.Close()
	gatewayURL := "https://gui-enrich-gateway.invalid/api/v1"
	u, _ := url.Parse(gatewayURL)
	defer llm.RegisterCatalogEnrichSource(u.Hostname(), catalog.URL)()

	eng, _, _, dir := statsFixture(t)
	writeDataConfig(t, dir, "[[providers]]\nname = \"cline\"\ntype = \"openai\"\nbase_url = \""+gatewayURL+"\"\napi_key = \"k\"\nmodel = \""+model+"\"\ncached_models = [\""+model+"\"]\n")
	eng.caps = llm.NewCapabilityRegistry()
	eng.cfg.Provider, eng.cfg.BaseURL, eng.cfg.APIKey, eng.cfg.Model = "openai", gatewayURL, "k", model

	if got := eng.statsActiveContext(eng.cfg); got.WindowSource != "fallback-remote" {
		t.Fatalf("precondition: %+v", got)
	}
	eng.enrichConfiguredProviderCatalogs(context.Background())
	got := eng.statsActiveContext(eng.cfg)
	if got.Window != 1050000 || got.WindowSource != "catalog" {
		t.Fatalf("active context = %+v, want catalog 1050000", got)
	}

	if err := eng.modelContexts.Set(got.Provider, model, 300000); err != nil {
		t.Fatal(err)
	}
	if got := eng.statsActiveContext(eng.cfg); got.Window != 300000 || got.WindowSource != "model-override" {
		t.Fatalf("override lost: %+v", got)
	}
}
