package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func TestProviderDiagnosticsReportsPassiveProbeAndLastCall(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen-local"},{"id":"embed-model"}]}`))
	}))
	defer upstream.Close()

	srv := newTestServer(t, false)
	m := srv.eng.providerManager()
	if err := m.Add("gpu-box", "openai", upstream.URL+"/v1", "secret", "qwen-local"); err != nil {
		t.Fatal(err)
	}
	srv.eng.recordProviderPerformance("gpu-box", llm.CallStat{
		Model: "qwen-local", TTFT: 250 * time.Millisecond, Duration: 2250 * time.Millisecond,
		TokensIn: 100, TokensOut: 20,
	})

	rec := httptest.NewRecorder()
	req := localProviderJSONRequest(http.MethodGet, "/api/provider/diagnostics?name=gpu-box", "")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got providerDiagnosticView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "online" || got.Scope != "local" || got.Server != "openai-compatible" {
		t.Fatalf("identity/status = %+v", got)
	}
	if len(got.Models) != 2 || got.SelectedModel != "qwen-local" {
		t.Fatalf("models = %+v, selected = %q", got.Models, got.SelectedModel)
	}
	if got.LastCall == nil || got.LastCall.TTFTMS != 250 || got.LastCall.TokensPerS != 10 {
		t.Fatalf("last call = %+v", got.LastCall)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatal("diagnostics leaked API key")
	}
}

func TestProviderDiagnosticsDisabledDoesNotContactEndpoint(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer upstream.Close()

	srv := newTestServer(t, false)
	m := srv.eng.providerManager()
	if err := m.Add("sleeping", "openai", upstream.URL+"/v1", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.SetDisabled("sleeping", true); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodGet, "/api/provider/diagnostics?name=sleeping", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"disabled"`) {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if hits.Load() != 0 {
		t.Fatalf("disabled endpoint contacted %d time(s)", hits.Load())
	}
}

func TestProviderDiagnosticsSanitizesRemoteErrorAndClassifiesScope(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "private upstream detail", http.StatusUnauthorized)
	}))
	defer upstream.Close()
	srv := newTestServer(t, false)
	if err := srv.eng.providerManager().Add("bad", "openai", upstream.URL+"/v1", "bad", ""); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodGet, "/api/provider/diagnostics?name=bad", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"error":"HTTP 401"`) {
		t.Fatalf("response = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "private upstream detail") {
		t.Fatal("upstream error body leaked")
	}
	if got := endpointScope("http://192.168.1.50:8080/v1"); got != "lan" {
		t.Fatalf("private IP scope = %q, want lan", got)
	}
	if got := endpointServer(providerConfForTest("llama-box", "http://192.168.1.50:8080/v1")); got != "llama.cpp" {
		t.Fatalf("server = %q, want llama.cpp", got)
	}
}

func providerConfForTest(name, baseURL string) config.ProviderConf {
	return config.ProviderConf{Name: name, Type: "openai", BaseURL: baseURL}
}
