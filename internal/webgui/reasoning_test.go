package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestHandleReasoning_UsesDiscoveredModelCapability(t *testing.T) {
	t.Cleanup(func() { _ = llm.SetReasoningEffort("") })
	srv := newTestServer(t, false)
	srv.eng.caps.Register(llm.ModelInfo{
		ID:        srv.eng.ModelName(),
		Reasoning: true,
		Source:    llm.SourceProvider,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/reasoning", strings.NewReader(`{"level":"high"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleReasoning(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/reasoning: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var got reasoningView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Supported || got.Configured != "high" || got.Effective != "high" {
		t.Fatalf("reasoning response = %+v", got)
	}
}

func TestHandleReasoning_ReturnsUpdatedState(t *testing.T) {
	t.Cleanup(func() { _ = llm.SetReasoningEffort("") })
	srv := newTestServer(t, false)
	srv.eng.caps.Register(llm.ModelInfo{
		ID:        srv.eng.ModelName(),
		Reasoning: true,
		Source:    llm.SourceProvider,
	})

	for _, level := range []string{"low", "default"} {
		req := httptest.NewRequest(http.MethodPost, "/api/reasoning", strings.NewReader(`{"level":"`+level+`"}`))
		rec := httptest.NewRecorder()
		srv.handleReasoning(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("level %s: status = %d, body = %s", level, rec.Code, rec.Body.String())
		}
		var got reasoningView
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		want := level
		if level == "default" {
			want = ""
		}
		if got.Configured != want {
			t.Fatalf("level %s: configured = %q, want %q", level, got.Configured, want)
		}
	}
}

func TestHandleReasoning_IsAvailableWithoutModelScan(t *testing.T) {
	t.Cleanup(func() { _ = llm.SetReasoningEffort("") })
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodGet, "/api/reasoning", nil)
	rec := httptest.NewRecorder()
	srv.handleReasoning(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/reasoning: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got reasoningView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Supported || len(got.Levels) == 0 {
		t.Fatalf("reasoning unavailable before scan: %+v", got)
	}
}
