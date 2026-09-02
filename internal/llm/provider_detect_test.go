package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetectProviderProtocolOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("authorization=%q", got)
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m","object":"model","owned_by":"local"}]}`))
	}))
	defer srv.Close()

	got, err := DetectProviderProtocol(context.Background(), srv.URL+"/v1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if got != "openai" {
		t.Fatalf("type=%q want openai", got)
	}
}

func TestDetectProviderProtocolAnthropicAfterOpenAIAuthRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-api-key") == "secret" && r.Header.Get("anthropic-version") != "" {
			_, _ = w.Write([]byte(`{"data":[{"type":"model","id":"claude-test","display_name":"Claude Test","created_at":"2026-01-01T00:00:00Z"}]}`))
			return
		}
		http.Error(w, "missing x-api-key", http.StatusUnauthorized)
	}))
	defer srv.Close()

	got, err := DetectProviderProtocol(context.Background(), srv.URL+"/v1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if got != "anthropic" {
		t.Fatalf("type=%q want anthropic", got)
	}
}

func TestDetectProviderProtocolUsesTerminalPathWithoutNetwork(t *testing.T) {
	cases := map[string]string{
		"https://example.test/v1/messages":         "anthropic",
		"https://example.test/v1/chat/completions": "openai",
		"https://example.test/v1/responses":        "responses",
	}
	for raw, want := range cases {
		got, err := DetectProviderProtocol(context.Background(), raw, "")
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if got != want {
			t.Fatalf("%s: type=%q want %q", raw, got, want)
		}
	}
}
