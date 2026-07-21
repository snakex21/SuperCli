package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supercli/internal/system/config"
)

func TestHandleModelContextScopesOverrideByProvider(t *testing.T) {
	dir := t.TempDir()
	store := config.LoadModelContextStore(dir)
	server := NewServer(&Engine{modelContexts: store}, false)

	req := httptest.NewRequest(http.MethodPost, "/api/model/context",
		strings.NewReader(`{"provider":"anyrouter","model":"gpt-5.6-sol","value":"100k"}`))
	rec := httptest.NewRecorder()
	server.handleModelContext(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("set context status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Tokens    int  `json:"tokens"`
		Automatic bool `json:"automatic"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Automatic || response.Tokens != 100_000 {
		t.Fatalf("response = %+v", response)
	}
	if got, ok := store.Get("anyrouter", "gpt-5.6-sol"); !ok || got != 100_000 {
		t.Fatalf("AnyRouter override = %d, %v", got, ok)
	}
	if _, ok := store.Get("openai", "gpt-5.6-sol"); ok {
		t.Fatal("AnyRouter override leaked into OpenAI")
	}

	req = httptest.NewRequest(http.MethodPost, "/api/model/context",
		strings.NewReader(`{"provider":"anyrouter","model":"gpt-5.6-sol","value":"auto"}`))
	rec = httptest.NewRecorder()
	server.handleModelContext(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset context status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, ok := store.Get("anyrouter", "gpt-5.6-sol"); ok {
		t.Fatal("auto did not remove exact override")
	}
}

func TestHandleModelContextRejectsInvalidValue(t *testing.T) {
	server := NewServer(&Engine{modelContexts: config.LoadModelContextStore(t.TempDir())}, false)
	req := httptest.NewRequest(http.MethodPost, "/api/model/context",
		strings.NewReader(`{"provider":"anyrouter","model":"gpt-5.6-sol","value":"a lot"}`))
	rec := httptest.NewRecorder()
	server.handleModelContext(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}
