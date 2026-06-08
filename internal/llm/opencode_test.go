package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewOpencode_EmptyAPIKey(t *testing.T) {
	p, err := NewOpencode(OpencodeConfig{
		BaseURL: "http://localhost:99999/v1",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("NewOpencode with empty APIKey: %v", err)
	}
	if p.Name() != "opencode/test-model" {
		t.Errorf("Name = %q, want opencode/test-model", p.Name())
	}
}

func TestNewOpencode_EmptyModel(t *testing.T) {
	p, err := NewOpencode(OpencodeConfig{
		BaseURL: "http://localhost:99999/v1",
	})
	if err != nil {
		t.Fatalf("NewOpencode with empty Model: %v", err)
	}
	if p.Name() != "opencode/default" {
		t.Errorf("Name = %q, want opencode/default", p.Name())
	}
}

func TestNewOpencode_AlreadyPrefixed(t *testing.T) {
	p, err := NewOpencode(OpencodeConfig{
		BaseURL: "http://localhost:99999/v1",
		Model:   "opencode/llama3",
	})
	if err != nil {
		t.Fatalf("NewOpencode: %v", err)
	}
	if p.Name() != "opencode/llama3" {
		t.Errorf("Name = %q, want opencode/llama3 (no double prefix)", p.Name())
	}
}

func TestNewOpencode_RawModelForGateway(t *testing.T) {
	p, err := NewOpencode(OpencodeConfig{
		BaseURL: "http://localhost:99999/v1",
		Model:   "llama3.2",
	})
	if err != nil {
		t.Fatalf("NewOpencode: %v", err)
	}
	if p.Name() != "opencode/llama3.2" {
		t.Errorf("Name = %q, want opencode/llama3.2", p.Name())
	}
	if p.rawModel != "llama3.2" {
		t.Errorf("rawModel = %q, want llama3.2", p.rawModel)
	}
}

func TestNewOpencode_AlreadyPrefixed_RawStripsPrefix(t *testing.T) {
	p, err := NewOpencode(OpencodeConfig{
		BaseURL: "http://localhost:99999/v1",
		Model:   "opencode/gemma2",
	})
	if err != nil {
		t.Fatalf("NewOpencode: %v", err)
	}
	if p.Name() != "opencode/gemma2" {
		t.Errorf("Name = %q, want opencode/gemma2", p.Name())
	}
	if p.rawModel != "gemma2" {
		t.Errorf("rawModel = %q, want gemma2 (prefix stripped)", p.rawModel)
	}
}

// Test that the inner OpenAI provider sends the RAW
// model name (not prefixed) in the request body.
func TestOpencodeProvider_CompleteSendsRawModel(t *testing.T) {
	var gotBody struct {
		Model string `json:"model"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"data":[]}`))
			return
		}
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	p, err := NewOpencode(OpencodeConfig{
		BaseURL: srv.URL,
		Model:   "llama3.2",
	})
	if err != nil {
		t.Fatalf("NewOpencode: %v", err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// Drain the channel to ensure the HTTP call completes
	// (the request is fired in a goroutine).
	for range ch {
	}
	// The gateway received the raw model name
	if gotBody.Model != "llama3.2" {
		t.Errorf("gateway received model=%q, want llama3.2 (raw, not prefixed)", gotBody.Model)
	}
}

func TestNewOpencode_DefaultBaseURL(t *testing.T) {
	p, err := NewOpencode(OpencodeConfig{
		Model: "test",
	})
	if err != nil {
		t.Fatalf("NewOpencode: %v", err)
	}
	if p.inner.cfg.BaseURL != DefaultOpencodeBaseURL {
		t.Errorf("BaseURL = %q, want %q", p.inner.cfg.BaseURL, DefaultOpencodeBaseURL)
	}
}

func TestNewOpencode_CustomBaseURL(t *testing.T) {
	p, err := NewOpencode(OpencodeConfig{
		BaseURL: "http://myhost:8080/v1/",
		Model:   "test",
	})
	if err != nil {
		t.Fatalf("NewOpencode: %v", err)
	}
	if p.inner.cfg.BaseURL != "http://myhost:8080/v1" {
		t.Errorf("BaseURL = %q, trailing slash not trimmed", p.inner.cfg.BaseURL)
	}
}

func TestOpencodeProvider_Name(t *testing.T) {
	p, _ := NewOpencode(OpencodeConfig{
		BaseURL: "http://localhost:99999/v1",
		Model:   "gemma2",
	})
	if p.Name() != "opencode/gemma2" {
		t.Errorf("Name = %q, want opencode/gemma2", p.Name())
	}
}

func TestProbeModels_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(opencodeModelsResponse{
			Data: []opencodeModelEntry{
				{ID: "llama3.2", Object: "model", OwnedBy: "ollama"},
				{ID: "gemma2", Object: "model", OwnedBy: "ollama"},
				{ID: "", Object: "model"}, // empty ID → filtered
			},
		})
	}))
	defer srv.Close()

	p, err := NewOpencode(OpencodeConfig{
		BaseURL: srv.URL + "/v1",
		Model:   "placeholder",
	})
	if err != nil {
		t.Fatalf("NewOpencode: %v", err)
	}

	models, err := p.ProbeModels(context.Background())
	if err != nil {
		t.Fatalf("ProbeModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("ProbeModels returned %d models, want 2", len(models))
	}
	if models[0].ID != "opencode/llama3.2" {
		t.Errorf("models[0].ID = %q, want opencode/llama3.2", models[0].ID)
	}
	if models[1].ID != "opencode/gemma2" {
		t.Errorf("models[1].ID = %q, want opencode/gemma2", models[1].ID)
	}
	if models[0].Source != SourceOpencode {
		t.Errorf("models[0].Source = %v, want SourceOpencode", models[0].Source)
	}
	if models[0].Provider != "opencode" {
		t.Errorf("models[0].Provider = %q, want opencode", models[0].Provider)
	}
}

func TestProbeModels_Unreachable(t *testing.T) {
	p, err := NewOpencode(OpencodeConfig{
		BaseURL: "http://127.0.0.1:1/v1",
		Model:   "test",
	})
	if err != nil {
		t.Fatalf("NewOpencode: %v", err)
	}
	models, err := p.ProbeModels(context.Background())
	if err != nil {
		t.Errorf("ProbeModels should return nil err for unreachable, got: %v", err)
	}
	if models != nil {
		t.Errorf("ProbeModels should return nil models for unreachable, got: %v", models)
	}
}

func TestProbeModels_Non200Status(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	p, err := NewOpencode(OpencodeConfig{
		BaseURL: srv.URL,
		Model:   "test",
	})
	if err != nil {
		t.Fatalf("NewOpencode: %v", err)
	}
	models, err := p.ProbeModels(context.Background())
	if err != nil {
		t.Errorf("ProbeModels should return nil err for 403, got: %v", err)
	}
	if models != nil {
		t.Errorf("ProbeModels should return nil for 403, got: %v", models)
	}
}

func TestProbeModels_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	p, err := NewOpencode(OpencodeConfig{
		BaseURL: srv.URL,
		Model:   "test",
	})
	if err != nil {
		t.Fatalf("NewOpencode: %v", err)
	}
	models, err := p.ProbeModels(context.Background())
	if err != nil {
		t.Errorf("ProbeModels should return nil err for bad JSON, got: %v", err)
	}
	if models != nil {
		t.Errorf("ProbeModels should return nil for bad JSON, got: %v", models)
	}
}

func TestProbeModels_RegistersInCapabilityRegistry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(opencodeModelsResponse{
			Data: []opencodeModelEntry{
				{ID: "tiny-llm", Object: "model"},
			},
		})
	}))
	defer srv.Close()

	caps := NewCapabilityRegistry()
	p, err := NewOpencode(OpencodeConfig{
		BaseURL:      srv.URL,
		Model:        "placeholder",
		Capabilities: caps,
	})
	if err != nil {
		t.Fatalf("NewOpencode: %v", err)
	}
	models, err := p.ProbeModels(context.Background())
	if err != nil {
		t.Fatalf("ProbeModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("want 1 model, got %d", len(models))
	}
	if !caps.IsConfigured("opencode/tiny-llm") {
		t.Error("ProbeModels should register in CapabilityRegistry")
	}
}

func TestProbeOpencodeModels_Standalone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(opencodeModelsResponse{
			Data: []opencodeModelEntry{
				{ID: "model-a"},
				{ID: "model-b"},
			},
		})
	}))
	defer srv.Close()

	entries, err := ProbeOpencodeModels(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatalf("ProbeOpencodeModels: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0].ID != "model-a" {
		t.Errorf("entries[0].ID = %q, want model-a", entries[0].ID)
	}
}

func TestProbeOpencodeModels_Unreachable(t *testing.T) {
	entries, err := ProbeOpencodeModels(context.Background(), "http://127.0.0.1:1/v1", "")
	if err == nil {
		t.Error("expected error for unreachable")
	}
	if entries != nil {
		t.Errorf("expected nil entries, got %v", entries)
	}
}

func TestOpencodeProvider_SupportsVision(t *testing.T) {
	p, _ := NewOpencode(OpencodeConfig{
		BaseURL: "http://localhost:99999/v1",
		Model:   "test",
	})
	if p.SupportsVision() {
		t.Error("SupportsVision should be false for unregistered model")
	}
}

func TestOpencodeProvider_SupportsVision_Registered(t *testing.T) {
	caps := NewCapabilityRegistry()
	caps.Register(ModelInfo{
		ID:       "opencode/vision-model",
		Provider: "opencode",
		Source:   SourceOpencode,
		Vision:   true,
	})
	p, _ := NewOpencode(OpencodeConfig{
		BaseURL:      "http://localhost:99999/v1",
		Model:        "opencode/vision-model",
		Capabilities: caps,
	})
	if !p.SupportsVision() {
		t.Error("SupportsVision should be true for registered vision model")
	}
}

func TestProbeModels_SkipsEmptyIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(opencodeModelsResponse{
			Data: []opencodeModelEntry{
				{ID: "   "}, // whitespace only
				{ID: ""},
				{ID: "valid"},
			},
		})
	}))
	defer srv.Close()

	p, _ := NewOpencode(OpencodeConfig{BaseURL: srv.URL, Model: "x"})
	models, err := p.ProbeModels(context.Background())
	if err != nil {
		t.Fatalf("ProbeModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("want 1 model, got %d", len(models))
	}
	if models[0].ID != "opencode/valid" {
		t.Errorf("models[0].ID = %q, want opencode/valid", models[0].ID)
	}
}

func TestNewOpencode_APIKeyPassthrough(t *testing.T) {
	p, _ := NewOpencode(OpencodeConfig{
		BaseURL: "http://localhost:99999/v1",
		Model:   "test",
		APIKey:  "sk-real-key",
	})
	if p.inner.cfg.APIKey != "sk-real-key" {
		t.Errorf("inner APIKey = %q, want sk-real-key", p.inner.cfg.APIKey)
	}
}

func TestNewOpencode_BaseURLPassthrough(t *testing.T) {
	p, _ := NewOpencode(OpencodeConfig{
		BaseURL: "http://custom-host:9090/v1",
		Model:   "test",
	})
	if p.inner.cfg.BaseURL != "http://custom-host:9090/v1" {
		t.Errorf("inner BaseURL = %q, want http://custom-host:9090/v1", p.inner.cfg.BaseURL)
	}
}
