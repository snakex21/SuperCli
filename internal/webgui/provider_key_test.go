package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const providerTestSecret = "sk-provider-super-secret"

func seedProviderSecret(t *testing.T, srv *Server) {
	t.Helper()
	m := srv.eng.providerManager()
	if err := m.Add("secret-provider", "openai", "https://example.test/v1", providerTestSecret, "model"); err != nil {
		t.Fatalf("Add provider: %v", err)
	}
}

func localProviderRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Host = "127.0.0.1:8765"
	req.RemoteAddr = "127.0.0.1:43210"
	return req
}

func localProviderJSONRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1:8765"
	req.RemoteAddr = "127.0.0.1:43210"
	return req
}

func TestProvidersListDoesNotExposeAPIKey(t *testing.T) {
	srv := newTestServer(t, false)
	seedProviderSecret(t, srv)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderRequest(http.MethodGet, "/api/providers"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, providerTestSecret) || strings.Contains(body, `"api_key"`) || strings.Contains(body, `"APIKey"`) {
		t.Fatalf("provider list exposed an API key: %s", body)
	}
	if !strings.Contains(body, `"HasKey":true`) {
		t.Fatalf("provider list lost the non-secret HasKey state: %s", body)
	}
}

func TestProviderKeyRevealAllowsLoopbackAndDisablesCaching(t *testing.T) {
	for _, allowRemote := range []bool{false, true} {
		t.Run(map[bool]string{false: "local-only", true: "allow-remote"}[allowRemote], func(t *testing.T) {
			srv := newTestServer(t, allowRemote)
			seedProviderSecret(t, srv)
			req := localProviderJSONRequest(http.MethodPost, "/api/provider/key/reveal", `{"name":"secret-provider"}`)
			req.RemoteAddr = "[::1]:43210"
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["api_key"] != providerTestSecret {
				t.Fatalf("api_key mismatch")
			}
			if !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
				t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
			}
			if got := rec.Header().Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
				t.Fatalf("Cross-Origin-Resource-Policy = %q", got)
			}
		})
	}
}

func TestProviderKeyRevealRejectsInvalidRequests(t *testing.T) {
	srv := newTestServer(t, false)
	seedProviderSecret(t, srv)
	tests := []struct {
		name   string
		method string
		body   string
		want   int
	}{
		{"wrong method", http.MethodGet, `{"name":"secret-provider"}`, http.StatusMethodNotAllowed},
		{"malformed", http.MethodPost, `{`, http.StatusBadRequest},
		{"blank name", http.MethodPost, `{"name":" "}`, http.StatusBadRequest},
		{"unknown provider", http.MethodPost, `{"name":"missing"}`, http.StatusNotFound},
		{"unknown field", http.MethodPost, `{"name":"secret-provider","extra":true}`, http.StatusBadRequest},
		{"trailing JSON", http.MethodPost, `{"name":"secret-provider"}{}`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, localProviderJSONRequest(tc.method, "/api/provider/key/reveal", tc.body))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tc.want, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), providerTestSecret) {
				t.Fatal("invalid request leaked the key")
			}
			if !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
				t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestProviderKeyRevealRejectsRemoteEvenWhenAllowed(t *testing.T) {
	srv := newTestServer(t, true)
	seedProviderSecret(t, srv)
	req := httptest.NewRequest(http.MethodPost, "/api/provider/key/reveal", strings.NewReader(`{"name":"secret-provider"}`))
	req.Host = "127.0.0.1:8765" // spoofed loopback Host must not be enough
	req.RemoteAddr = "203.0.113.9:43210"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if strings.Contains(rec.Body.String(), providerTestSecret) {
		t.Fatal("remote rejection leaked the key")
	}
}

func TestProviderKeyRevealRejectsDNSRebindingHost(t *testing.T) {
	srv := newTestServer(t, true)
	seedProviderSecret(t, srv)
	req := httptest.NewRequest(http.MethodPost, "/api/provider/key/reveal", strings.NewReader(`{"name":"secret-provider"}`))
	req.Host = "evil.example"
	req.RemoteAddr = "127.0.0.1:43210"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestIsLoopbackRemoteAddr(t *testing.T) {
	tests := map[string]bool{
		"127.0.0.1:1":        true,
		"[::1]:9000":         true,
		"::1":                false,
		"203.0.113.4:1":      false,
		"192.168.1.5:5000":   false,
		"not-an-address:123": false,
		"":                   false,
	}
	for addr, want := range tests {
		if got := isLoopbackRemoteAddr(addr); got != want {
			t.Errorf("isLoopbackRemoteAddr(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestProvidersUpdateAPIKeyPresenceOverHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model"}]}`))
	}))
	defer upstream.Close()

	srv := newTestServer(t, false)
	m := srv.eng.providerManager()
	if err := m.Add("editable", "openai", upstream.URL+"/v1", providerTestSecret, "model"); err != nil {
		t.Fatal(err)
	}

	update := func(body string, want int) {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodPut, "/api/providers", body))
		if rec.Code != want {
			t.Fatalf("status = %d, want %d; body = %s", rec.Code, want, rec.Body.String())
		}
	}

	update(`{"name":"editable","model":"model-2"}`, http.StatusOK)
	m.Reload()
	if got, ok := m.APIKey("editable"); !ok || got != providerTestSecret {
		t.Fatalf("omitted API key = %q, ok=%v; want preserved", got, ok)
	}

	update(`{"name":"editable","api_key":"Bearer "}`, http.StatusBadRequest)
	m.Reload()
	if got, ok := m.APIKey("editable"); !ok || got != providerTestSecret {
		t.Fatalf("invalid API key = %q, ok=%v; want preserved", got, ok)
	}

	update(`{"name":"editable","api_key":""}`, http.StatusOK)
	m.Reload()
	if got, ok := m.APIKey("editable"); !ok || got != "" {
		t.Fatalf("explicit empty API key = %q, ok=%v; want cleared", got, ok)
	}
}
