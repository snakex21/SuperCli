package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func catalogFixture(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"openai/gpt-fixture","context_length":1050000,"pricing":{"prompt":"0.000002","completion":"0.00001"}},
			{"id":"vendor/top-only","top_provider":{"context_length":200000},"pricing":{"prompt":"-1","completion":"-1"}}
		]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestEnrichModelInfosFillsMissingContextFromMappedCatalog(t *testing.T) {
	var hits int32
	catalog := catalogFixture(t, &hits)
	gateway := "https://gateway-enrich.invalid/api/v1"
	t.Cleanup(RegisterCatalogEnrichSource("gateway-enrich.invalid", catalog.URL))

	in := []ModelInfo{{ID: "openai/gpt-fixture"}, {ID: "vendor/top-only"}, {ID: "unknown/model"}, {ID: "kept", ContextLength: 4096}}
	out := EnrichModelInfos(context.Background(), gateway, in)
	if out[0].ContextLength != 1050000 || out[0].InputCost != 2 || out[0].OutputCost != 10 {
		t.Fatalf("exact id not enriched: %+v", out[0])
	}
	if out[1].ContextLength != 200000 || out[1].InputCost != 0 {
		t.Fatalf("top_provider fallback / negative price: %+v", out[1])
	}
	if out[2].ContextLength != 0 || out[3].ContextLength != 4096 {
		t.Fatalf("unmatched or known rows changed: %+v", out[2:])
	}
	if in[0].ContextLength != 0 {
		t.Fatal("input slice mutated")
	}
	_ = EnrichModelInfos(context.Background(), gateway, in)
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("catalog fetched %d times, want 1 (cached)", got)
	}
}

func TestEnrichModelInfosUnmappedHostAndFailureAreNoOps(t *testing.T) {
	var hits int32
	catalog := catalogFixture(t, &hits)
	in := []ModelInfo{{ID: "openai/gpt-fixture"}}
	if out := EnrichModelInfos(context.Background(), "https://not-mapped.invalid/v1", in); out[0].ContextLength != 0 || hits != 0 {
		t.Fatalf("unmapped host enriched or fetched: %+v hits=%d", out, hits)
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer broken.Close()
	t.Cleanup(RegisterCatalogEnrichSource("broken-enrich.invalid", broken.URL))
	if out := EnrichModelInfos(context.Background(), "https://broken-enrich.invalid/v1", in); out[0].ContextLength != 0 {
		t.Fatalf("failed catalog changed data: %+v", out)
	}
	_ = catalog
}

func TestClineHostIsMappedToOpenRouterCatalog(t *testing.T) {
	got := CatalogEnrichmentURL("https://api.cline.bot/api/v1")
	if u, err := url.Parse(got); err != nil || u.Host != "openrouter.ai" {
		t.Fatalf("cline enrichment url = %q", got)
	}
}
