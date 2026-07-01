package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsLoopbackHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"localhost:8080", true},
		{"127.0.0.1", true},
		{"127.0.0.1:5000", true},
		{"::1", true},
		{"[::1]:9000", true},
		{"", true},
		{"example.com", false},
		{"example.com:80", false},
		{"10.0.0.5", false},
		{"192.168.1.10:3000", false},
	}
	for _, c := range cases {
		if got := isLoopbackHost(c.host); got != c.want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestQueryInt(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?a=5&b=abc&c=-3&d=0", nil)
	if got := queryInt(r, "a", 10); got != 5 {
		t.Errorf("a = %d, want 5", got)
	}
	if got := queryInt(r, "b", 10); got != 10 {
		t.Errorf("b malformed = %d, want default 10", got)
	}
	if got := queryInt(r, "c", 10); got != 10 {
		t.Errorf("c negative = %d, want default 10", got)
	}
	if got := queryInt(r, "d", 10); got != 10 {
		t.Errorf("d zero = %d, want default 10", got)
	}
	if got := queryInt(r, "missing", 7); got != 7 {
		t.Errorf("missing = %d, want default 7", got)
	}
}

// newTestServer builds a Server backed by an echo engine in a temp
// data dir — no network, no API key, deterministic.
func newTestServer(t *testing.T, allowRemote bool) *Server {
	t.Helper()
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return NewServer(eng, allowRemote)
}

func TestLocalGuard_BlocksRemoteHost(t *testing.T) {
	srv := newTestServer(t, false)
	h := srv.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("remote host: status = %d, want 403", rec.Code)
	}
}

func TestLocalGuard_AllowsLoopback(t *testing.T) {
	srv := newTestServer(t, false)
	h := srv.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Host = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("loopback: status = %d, want 200", rec.Code)
	}
}

func TestLocalGuard_AllowRemoteBypass(t *testing.T) {
	srv := newTestServer(t, true)
	h := srv.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("allowRemote: status = %d, want 200", rec.Code)
	}
}

func TestHandleHealth(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	srv.handleHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
}

func TestHandleTranscript_MissingID(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodGet, "/api/transcript", nil)
	rec := httptest.NewRecorder()
	srv.handleTranscript(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing id: status = %d, want 400", rec.Code)
	}
}
