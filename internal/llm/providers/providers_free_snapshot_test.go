package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
)

func TestFreeProviderScanUsesOneCatalogSnapshot(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/models" || r.URL.Path == "/api/v0/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[]}`))
			return
		}
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		contextLength := 64000 * int(calls.Add(1))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"id": "paid", "context_length": 100000},
			{"id": "zero-preview", "isFree": false, "pricing": map[string]any{"prompt": "0", "completion": "0"}},
			{"id": "explicit-free", "context_length": contextLength, "architecture": map[string]any{"input_modalities": []string{"text", "image"}}, "capabilities": map[string]any{"reasoning": true}},
			{"id": "free-alias", "isFree": true, "context_length": 32000, "architecture": map[string]any{"input_modalities": []string{"text"}}},
		}})
	}))
	defer srv.Close()
	m := NewManager(t.TempDir())
	if err := m.Add("kilo", "openai", srv.URL+"/v1", "", ""); err != nil {
		t.Fatal(err)
	}
	caps := llm.NewCapabilityRegistry()
	res := m.ScanProvider("kilo", caps)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if len(res.Models) != 2 {
		t.Fatalf("free models: %v", res.Models)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("catalog requests = %d, want 1", got)
	}
	vision, ok := caps.Get("explicit-free")
	if !ok || vision.ContextLength != 64000 || !vision.VisionKnown || !vision.Vision || !vision.ReasoningKnown || !vision.Reasoning {
		t.Fatalf("missing free model metadata: %+v", vision)
	}
	text, ok := caps.Get("free-alias")
	if !ok || text.ContextLength != 32000 || !text.VisionKnown || text.Vision {
		t.Fatalf("text-only metadata changed: %+v", text)
	}
	if _, ok := caps.Get("paid"); ok {
		t.Fatal("paid model registered")
	}
	if _, ok := caps.Get("zero-preview"); ok {
		t.Fatal("explicit non-free preview registered")
	}
	// Public discovery remains fresh. The changed response cannot be paired with
	// metadata retained from an earlier scan.
	if res = m.ScanProvider("kilo", caps); res.Err != nil {
		t.Fatal(res.Err)
	}
	vision, _ = caps.Get("explicit-free")
	if vision.ContextLength != 128000 {
		t.Errorf("metadata from the second response = %d, want 128000", vision.ContextLength)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("two scans made %d requests, want 2", got)
	}
}
