package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponsesCompleteUsesAPIKeyWithoutChatGPTHeaders(t *testing.T) {
	var gotReq map[string]any
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.NotFound(w, r)
			return
		}
		gotHeaders = r.Header.Clone()
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		codexSSE(w,
			`{"type":"response.output_text.delta","delta":"OK"}`,
			`{"type":"response.completed","response":{"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
		)
	}))
	defer srv.Close()

	p, err := NewResponses(ResponsesConfig{
		BaseURL: srv.URL,
		APIKey:  "  Bearer test-key\r\n",
		Model:   "gpt-5.6-sol",
	})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("delta error: %v", d.Err)
		}
		text.WriteString(d.Content)
	}
	if text.String() != "OK" {
		t.Fatalf("text = %q, want OK", text.String())
	}
	if got := gotHeaders.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want cleaned bearer", got)
	}
	for _, name := range []string{"OpenAI-Beta", "originator", "chatgpt-account-id"} {
		if got := gotHeaders.Get(name); got != "" {
			t.Errorf("%s leaked ChatGPT-specific value %q", name, got)
		}
	}
	if gotReq["model"] != "gpt-5.6-sol" || gotReq["stream"] != true {
		t.Fatalf("request = %#v", gotReq)
	}
	reasoning, _ := gotReq["reasoning"].(map[string]any)
	if reasoning["effort"] != "medium" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %#v", reasoning)
	}
	include, _ := gotReq["include"].([]any)
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v", gotReq["include"])
	}
	if key, _ := gotReq["prompt_cache_key"].(string); len(key) != 36 || strings.Count(key, "-") != 4 {
		t.Fatalf("prompt_cache_key = %q", key)
	}
	if tools, ok := gotReq["tools"].([]any); !ok || len(tools) != 0 {
		t.Fatalf("tools = %#v, want explicit empty array", gotReq["tools"])
	}
}

func TestPrepareStandardResponsesSkipsReasoningFieldsForPlainModel(t *testing.T) {
	body, err := prepareStandardResponsesRequest([]byte(`{"model":"plain","include":[]}`), "cache", false, Sampling{})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["reasoning"]; ok {
		t.Fatalf("plain model received reasoning: %#v", got)
	}
	include, _ := got["include"].([]any)
	if len(include) != 0 {
		t.Fatalf("plain model include = %#v", got["include"])
	}
	if got["prompt_cache_key"] != "cache" {
		t.Fatalf("prompt_cache_key = %#v", got["prompt_cache_key"])
	}
}

func TestResponsesUnauthorizedDoesNotRetryStaticKey(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	p, err := NewResponses(ResponsesConfig{BaseURL: srv.URL, APIKey: "bad", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var gotErr error
	for d := range ch {
		if d.Err != nil {
			gotErr = d.Err
		}
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "http 401") {
		t.Fatalf("error = %v, want HTTP 401", gotErr)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want one request for a static key", calls)
	}
}

func TestResponsesStreamErrorEventEmitsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		codexSSE(w, `{"type":"error","code":"server_error","message":"upstream failed"}`)
	}))
	defer srv.Close()

	p, err := NewResponses(ResponsesConfig{BaseURL: srv.URL, APIKey: "key", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got error
	for d := range ch {
		if d.Err != nil {
			got = d.Err
		}
	}
	if got == nil || !strings.Contains(got.Error(), "upstream failed") {
		t.Fatalf("error = %v, want Responses stream error", got)
	}
}

func TestResponsesTruncatedStreamEmitsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		codexSSE(w, `{"type":"response.output_text.delta","delta":"partial"}`)
	}))
	defer srv.Close()

	p, err := NewResponses(ResponsesConfig{BaseURL: srv.URL, APIKey: "key", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got error
	for d := range ch {
		if d.Err != nil {
			got = d.Err
		}
	}
	if got == nil || !strings.Contains(got.Error(), "before response.completed") {
		t.Fatalf("error = %v, want truncated stream error", got)
	}
}
