package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDetectLocalServersBothUp(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"models":[{"name":"qwen2.5:7b"},{"name":"llama3.2:3b"}]}`))
	}))
	defer ollama.Close()
	lmstudio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"data":[{"id":"qwen2.5-coder-7b-instruct"}]}`))
	}))
	defer lmstudio.Close()

	found := detectLocalServers(context.Background(), ollama.URL, lmstudio.URL)
	if len(found) != 2 {
		t.Fatalf("expected 2 servers, got %d: %+v", len(found), found)
	}
	if found[0].Name != "ollama" || found[1].Name != "lmstudio" {
		t.Fatalf("expected order ollama,lmstudio got %s,%s", found[0].Name, found[1].Name)
	}
	if len(found[0].Models) != 2 || found[0].Models[0] != "llama3.2:3b" {
		t.Errorf("ollama models = %v", found[0].Models)
	}
	if !strings.HasSuffix(found[0].BaseURL, "/v1") {
		t.Errorf("ollama BaseURL should end with /v1, got %s", found[0].BaseURL)
	}
	if len(found[1].Models) != 1 || found[1].Models[0] != "qwen2.5-coder-7b-instruct" {
		t.Errorf("lmstudio models = %v", found[1].Models)
	}
}

func TestDetectLocalServersNoneUp(t *testing.T) {
	// Closed ports: use a server we immediately close.
	s := httptest.NewServer(http.NotFoundHandler())
	url := s.URL
	s.Close()
	found := detectLocalServers(context.Background(), url, url)
	if len(found) != 0 {
		t.Fatalf("expected no servers, got %+v", found)
	}
}

func TestDetectLocalServersBadJSON(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>not json</html>`))
	}))
	defer bad.Close()
	found := detectLocalServers(context.Background(), bad.URL, bad.URL)
	if len(found) != 0 {
		t.Fatalf("expected no servers on bad JSON, got %+v", found)
	}
}

func TestDetectLocalServersOllamaOnly(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Write([]byte(`{"models":[{"name":"phi3:mini"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ollama.Close()
	down := httptest.NewServer(http.NotFoundHandler())
	downURL := down.URL
	down.Close()

	found := detectLocalServers(context.Background(), ollama.URL, downURL)
	if len(found) != 1 || found[0].Name != "ollama" {
		t.Fatalf("expected ollama only, got %+v", found)
	}
}

func TestHumanizeVerifyError(t *testing.T) {
	cases := []struct {
		raw, key, want string
	}{
		{"status 401: unauthorized", "", "requires an API key"},
		{"status 401: unauthorized", "sk-x", "rejected"},
		{"status 404: no", "k", "base URL"},
		{"dial tcp 127.0.0.1:1234: connectex: No connection could be made", "", "is the server running"},
		// A timeout must name the budget it blew and say the entry survives,
		// so the user knows to retry rather than assuming a rejection.
		{"context deadline exceeded", "", "did not answer within 1m0s"},
		{"context deadline exceeded", "", "retry the scan"},
	}
	for _, c := range cases {
		err := humanizeVerifyError(c.raw, "http://localhost:1234/v1", c.key)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("humanize(%q) = %v, want substring %q", c.raw, err, c.want)
		}
	}
}
