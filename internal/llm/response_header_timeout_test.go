package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProvidersBoundResponseHeaderWaitWithCustomClient(t *testing.T) {
	newSlowServer := func(t *testing.T) *httptest.Server {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(120 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(server.Close)
		return server
	}
	assertTimeout := func(t *testing.T, deltas <-chan Delta) {
		t.Helper()
		var streamErr error
		for delta := range deltas {
			if delta.Err != nil {
				streamErr = delta.Err
			}
		}
		if streamErr == nil || !strings.Contains(streamErr.Error(), "response headers timeout") {
			t.Fatalf("expected response-header timeout, got %v", streamErr)
		}
	}

	t.Run("openai", func(t *testing.T) {
		server := newSlowServer(t)
		provider, err := NewOpenAI(OpenAIConfig{BaseURL: server.URL, Model: "model", Timeout: 20 * time.Millisecond, HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		deltas, err := provider.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		assertTimeout(t, deltas)
	})

	t.Run("anthropic", func(t *testing.T) {
		server := newSlowServer(t)
		provider, err := NewAnthropic(AnthropicConfig{BaseURL: server.URL, APIKey: "key", Model: "model", Timeout: 20 * time.Millisecond, HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		deltas, err := provider.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		assertTimeout(t, deltas)
	})

	t.Run("responses", func(t *testing.T) {
		server := newSlowServer(t)
		provider, err := NewCodex(CodexConfig{BackendURL: server.URL, Model: "model", Tokens: &fakeTokens{access: "token"}, Timeout: 20 * time.Millisecond, HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		deltas, err := provider.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		assertTimeout(t, deltas)
	})
}
