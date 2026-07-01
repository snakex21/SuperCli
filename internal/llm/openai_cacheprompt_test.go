package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// These tests pin the cache_prompt gate: llama.cpp-family KV-cache
// hinting must reach local/private servers and must NEVER reach cloud
// endpoints (OpenAI rejects unknown request fields with HTTP 400).

func TestIsLocalBaseURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"http://localhost:8080/v1", true},
		{"http://LOCALHOST:8080", true},
		{"http://llama.localhost:8080", true},
		{"http://127.0.0.1:8080/v1", true},
		{"http://127.0.0.53:9999", true},
		{"http://[::1]:8080/v1", true},
		{"http://0.0.0.0:8000", true},
		{"http://192.168.1.44:1234/v1", true},
		{"http://10.0.0.7:8000/v1", true},
		{"http://172.16.5.9:5000", true},
		{"http://169.254.10.10:8080", true},
		{"https://api.openai.com/v1", false},
		{"https://openrouter.ai/api/v1", false},
		{"https://api.together.xyz/v1", false},
		{"http://8.8.8.8:8080", false},
		{"http://my-llama-box.example.com:8080", false}, // public hostname: opt in via CachePrompt
		{"", false},
		{"::not-a-url::", false},
	}
	for _, c := range cases {
		if got := isLocalBaseURL(c.url); got != c.want {
			t.Errorf("isLocalBaseURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// captureRequestBody runs one Complete round-trip against a local
// httptest server (which listens on 127.0.0.1) and returns the raw
// JSON request body the provider sent.
func captureRequestBody(t *testing.T, cfg OpenAIConfig) map[string]json.RawMessage {
	t.Helper()
	srv, captured := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w,
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		)
	})
	cfg.BaseURL = srv.URL
	p, err := NewOpenAI(cfg)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	drainDeltas(t, ch)
	var req map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*captured), &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	return req
}

func TestCachePrompt_LocalBaseURL_FieldPresent(t *testing.T) {
	// httptest server URL is http://127.0.0.1:<port> -> auto-detected
	// as local -> cache_prompt:true must be on the wire.
	req := captureRequestBody(t, OpenAIConfig{Model: "qwen-local"})
	raw, ok := req["cache_prompt"]
	if !ok {
		t.Fatal("cache_prompt missing from request to local server")
	}
	if string(raw) != "true" {
		t.Fatalf("cache_prompt = %s, want true", raw)
	}
}

func TestCachePrompt_ExplicitOff_FieldAbsent(t *testing.T) {
	off := false
	req := captureRequestBody(t, OpenAIConfig{Model: "qwen-local", CachePrompt: &off})
	if _, ok := req["cache_prompt"]; ok {
		t.Fatal("cache_prompt present despite explicit CachePrompt=false")
	}
}

func TestCachePrompt_CloudBaseURL_NeverResolvesOn(t *testing.T) {
	// Cloud endpoints must not get the field. We cannot hit the real
	// endpoint in a test, so pin the resolved provider flag (the only
	// input to the request builder) plus the builder output itself.
	p, err := NewOpenAI(OpenAIConfig{BaseURL: "https://api.openai.com/v1", APIKey: "k", Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	if p.cachePrompt {
		t.Fatal("cachePrompt resolved true for api.openai.com")
	}
	body, err := buildOpenAIRequest("gpt-4o", []Message{{Role: RoleUser, Content: "hi"}}, nil, false, p.cachePrompt)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if strings.Contains(string(body), "cache_prompt") {
		t.Fatalf("cloud request body contains cache_prompt: %s", body)
	}
}

func TestCachePrompt_ExplicitOn_OverridesCloudDetection(t *testing.T) {
	// A llama.cpp box behind a public hostname can opt in explicitly.
	on := true
	p, err := NewOpenAI(OpenAIConfig{BaseURL: "https://my-llama-box.example.com/v1", Model: "qwen-local", CachePrompt: &on})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	if !p.cachePrompt {
		t.Fatal("explicit CachePrompt=true ignored")
	}
}
