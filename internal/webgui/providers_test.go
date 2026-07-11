package webgui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProvidersAddRollsBackWhenVerificationFails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid API key", http.StatusUnauthorized)
	}))
	defer upstream.Close()

	srv := newTestServer(t, false)
	body := `{"name":"broken","type":"openai","base_url":"` + upstream.URL + `/v1","api_key":"bad"}`
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodPost, "/api/providers", body))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "provider was not added") {
		t.Fatalf("response does not explain rollback: %s", rec.Body.String())
	}
	if got := srv.eng.providerManager().Names(); len(got) != 0 {
		t.Fatalf("failed provider remained configured: %v", got)
	}
	// Confirm the rollback was persisted, not just applied in memory.
	srv.eng.providerManager().Reload()
	if got := srv.eng.providerManager().Names(); len(got) != 0 {
		t.Fatalf("failed provider remained after reload: %v", got)
	}
}

func TestProvidersAddKeepsVerifiedProvider(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
	}))
	defer upstream.Close()

	srv := newTestServer(t, false)
	body := `{"name":"working","type":"openai","base_url":"` + upstream.URL + `/v1","api_key":"key"}`
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodPost, "/api/providers", body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := srv.eng.providerManager().Names(); len(got) != 1 || got[0] != "working" {
		t.Fatalf("configured providers = %v, want [working]", got)
	}
}
