package providers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"supercli/internal/llm"
)

// A gateway whose /models carries only ids gets its context windows from the
// mapped public catalog during the scan, registered under the gateway's ids.
func TestScanProviderEnrichesContextFromMappedCatalog(t *testing.T) {
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"openai/gpt-scan-fixture","context_length":1050000}]}`))
	}))
	defer catalog.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"openai/gpt-scan-fixture","object":"model","created":0,"owned_by":"cline"}]}`))
	}))
	defer gateway.Close()
	u, _ := url.Parse(gateway.URL)
	defer llm.RegisterCatalogEnrichSource(u.Hostname(), catalog.URL)()

	m := NewManager(t.TempDir())
	m.Add("cline-fixture", "openai", gateway.URL+"/api/v1", "key", "")
	m.Reload()
	caps := llm.NewCapabilityRegistry()
	if res := m.ScanProvider("cline-fixture", caps); res.Err != nil {
		t.Fatal(res.Err)
	}
	mi, ok := caps.Get("openai/gpt-scan-fixture")
	if !ok || mi.ContextLength != 1050000 || mi.Provider != "cline-fixture" {
		t.Fatalf("scanned model = %+v ok=%v", mi, ok)
	}
}

// A rescan must not wipe a context window another source already established.
func TestScanProviderKeepsExistingContextWhenListOmitsIt(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"bare-model"}]}`))
	}))
	defer gateway.Close()
	m := NewManager(t.TempDir())
	m.Add("bare", "openai", gateway.URL+"/v1", "", "")
	m.Reload()
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "bare-model", ContextLength: 400000, Source: llm.SourceExternal})
	if res := m.ScanProvider("bare", caps); res.Err != nil {
		t.Fatal(res.Err)
	}
	if mi, _ := caps.Get("bare-model"); mi.ContextLength != 400000 || mi.Provider != "bare" {
		t.Fatalf("rescan dropped metadata: %+v", mi)
	}
}
