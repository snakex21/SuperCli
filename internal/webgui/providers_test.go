package webgui

import (
	"encoding/json"
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

func TestModelVisibilityBatchEndpoint(t *testing.T) {
	srv := newTestServer(t, false)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodPost, "/api/model/visibility", `{"refs":[{"provider":"x","model":"shared"},{"provider":"x","model":"m2"},{"provider":"x","model":"shared"}],"hidden":true}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Changed int `json:"changed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Changed != 2 {
		t.Fatalf("changed = %d, want 2", response.Changed)
	}
	m := srv.eng.providerManager()
	if !m.IsHiddenFor("x", "shared") || !m.IsHiddenFor("x", "m2") {
		t.Fatal("models were not hidden")
	}
	if m.IsHiddenFor("y", "shared") {
		t.Fatal("batch visibility leaked to another provider with the same model ID")
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

func TestProvidersDisableKeepsConfigurationAndRemovesModels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"remote-model"}]}`))
	}))
	defer upstream.Close()

	srv := newTestServer(t, false)
	add := `{"name":"sometimes-online","type":"openai","base_url":"` + upstream.URL + `/v1","api_key":"key"}`
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodPost, "/api/providers", add))
	if rec.Code != http.StatusOK {
		t.Fatalf("add status = %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodPut, "/api/providers", `{"name":"sometimes-online","disabled":true}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("disable status = %d: %s", rec.Code, rec.Body.String())
	}
	m := srv.eng.providerManager()
	m.Reload()
	configured := m.Configured()
	if len(configured) != 1 || !configured[0].Disabled || configured[0].APIKey != "key" {
		t.Fatalf("disabled provider was not preserved: %+v", configured)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodGet, "/api/models", ""))
	if strings.Contains(rec.Body.String(), "remote-model") {
		t.Fatalf("disabled provider model leaked into picker: %s", rec.Body.String())
	}
}

func TestProvidersUpdateRefreshesActiveRuntime(t *testing.T) {
	oldUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"remote-model"}]}`))
	}))
	defer oldUpstream.Close()
	newUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"remote-model"}]}`))
	}))
	defer newUpstream.Close()

	srv := newTestServer(t, false)
	add := `{"name":"remote","type":"openai","base_url":"` + oldUpstream.URL + `/v1","api_key":"key","model":"remote-model"}`
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodPost, "/api/providers", add))
	if rec.Code != http.StatusOK {
		t.Fatalf("add status = %d: %s", rec.Code, rec.Body.String())
	}
	if err := srv.eng.SwitchModel("remote-model", "remote"); err != nil {
		t.Fatalf("activate provider: %v", err)
	}

	update := `{"name":"remote","base_url":"` + newUpstream.URL + `/v1"}`
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodPut, "/api/providers", update))
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		RuntimeRefreshed bool `json:"runtime_refreshed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.RuntimeRefreshed {
		t.Fatalf("active runtime was not reported as refreshed: %s", rec.Body.String())
	}

	srv.eng.mu.RLock()
	activeURL := srv.eng.cfg.BaseURL
	srv.eng.mu.RUnlock()
	if activeURL != newUpstream.URL+"/v1" {
		t.Fatalf("active runtime URL = %q, want %q", activeURL, newUpstream.URL+"/v1")
	}
}

func TestClearingPublicGatewayKeyRemovesPaidModelsFromPicker(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"paid-model","isFree":false},{"id":"free-model","isFree":true}]}`))
	}))
	defer upstream.Close()

	srv := newTestServer(t, false)
	add := `{"name":"opencode","type":"openai","base_url":"` + upstream.URL + `/v1","api_key":"user-key","model":"paid-model"}`
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodPost, "/api/providers", add))
	if rec.Code != http.StatusOK {
		t.Fatalf("add status = %d: %s", rec.Code, rec.Body.String())
	}

	// An explicit empty string means "remove the saved key". The following
	// scan must switch to public/free-only discovery, even though paid entries
	// remain in the shared capability registry from the previous scan.
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodPut, "/api/providers", `{"name":"opencode","api_key":""}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear key status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodGet, "/api/models", ""))
	if strings.Contains(rec.Body.String(), "paid-model") {
		t.Fatalf("paid model survived key removal: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "free-model") {
		t.Fatalf("free model missing after key removal: %s", rec.Body.String())
	}
}
