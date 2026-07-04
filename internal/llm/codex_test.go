package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeTokens implements CodexTokenSource for tests.
type fakeTokens struct {
	access    string
	accountID string
	refreshed int
}

func (f *fakeTokens) Token(ctx context.Context) (string, string, error) {
	return f.access, f.accountID, nil
}

func (f *fakeTokens) Refresh(ctx context.Context) (string, error) {
	f.refreshed++
	f.access = "refreshed-token"
	return f.access, nil
}

func codexSSE(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		fmt.Fprintf(w, "data: %s\n\n", e)
	}
}

func TestCodexCompleteStreamsTextToolsAndUsage(t *testing.T) {
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
			`{"type":"response.output_text.delta","delta":"Hel"}`,
			`{"type":"response.output_text.delta","delta":"lo"}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","name":"read_file","arguments":"{\"path\":\"x\"}","call_id":"call_1"}}`,
			`{"type":"response.completed","response":{"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}}`,
		)
	}))
	defer srv.Close()

	toks := &fakeTokens{access: "tok-1", accountID: "acc-1"}
	p, err := NewCodex(CodexConfig{BackendURL: srv.URL, Model: "gpt-5-codex", Tokens: toks})
	if err != nil {
		t.Fatal(err)
	}
	msgs := []Message{
		{Role: RoleSystem, Content: "be helpful"},
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c0", Name: "ls", Arguments: "{}"}}},
		{Role: RoleTool, ToolCallID: "c0", Content: "ok"},
	}
	tools := []ToolDef{{Name: "read_file", Description: "read", Schema: `{"type":"object"}`}}
	ch, err := p.Complete(context.Background(), msgs, tools)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var toolCalls []ToolCall
	var usage *Usage
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("delta error: %v", d.Err)
		}
		text.WriteString(d.Content)
		if d.ToolCall != nil {
			toolCalls = append(toolCalls, *d.ToolCall)
		}
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	if text.String() != "Hello" {
		t.Fatalf("text = %q, want Hello", text.String())
	}
	if len(toolCalls) != 1 || toolCalls[0].Name != "read_file" || toolCalls[0].ID != "call_1" {
		t.Fatalf("tool calls = %+v", toolCalls)
	}
	if usage == nil || usage.Input != 11 || usage.Output != 7 {
		t.Fatalf("usage = %+v", usage)
	}

	// Headers required by the ChatGPT backend.
	if got := gotHeaders.Get("Authorization"); got != "Bearer tok-1" {
		t.Errorf("Authorization = %q", got)
	}
	if got := gotHeaders.Get("chatgpt-account-id"); got != "acc-1" {
		t.Errorf("chatgpt-account-id = %q", got)
	}
	if got := gotHeaders.Get("OpenAI-Beta"); got != "responses=experimental" {
		t.Errorf("OpenAI-Beta = %q", got)
	}

	// Request translation: instructions from system, items for
	// user / function_call / function_call_output.
	if gotReq["instructions"] != "be helpful" {
		t.Errorf("instructions = %v", gotReq["instructions"])
	}
	input := gotReq["input"].([]any)
	types := make([]string, 0, len(input))
	for _, it := range input {
		types = append(types, it.(map[string]any)["type"].(string))
	}
	want := []string{"message", "function_call", "function_call_output"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Errorf("input item types = %v, want %v", types, want)
	}
	if gotReq["stream"] != true || gotReq["store"] != false {
		t.Errorf("stream/store flags wrong: stream=%v store=%v", gotReq["stream"], gotReq["store"])
	}
	toolsArr := gotReq["tools"].([]any)
	tool0 := toolsArr[0].(map[string]any)
	if tool0["type"] != "function" || tool0["name"] != "read_file" {
		t.Errorf("tool decl = %v", tool0)
	}
}

func TestCodexRefreshesOn401(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer refreshed-token" {
			http.Error(w, `{"detail":"token expired"}`, http.StatusUnauthorized)
			return
		}
		codexSSE(w,
			`{"type":"response.output_text.delta","delta":"ok"}`,
			`{"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		)
	}))
	defer srv.Close()

	toks := &fakeTokens{access: "stale", accountID: "acc"}
	p, err := NewCodex(CodexConfig{BackendURL: srv.URL, Model: "gpt-5-codex", Tokens: toks})
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
	if text.String() != "ok" {
		t.Fatalf("text = %q, want ok", text.String())
	}
	if toks.refreshed != 1 {
		t.Fatalf("refreshed %d times, want 1", toks.refreshed)
	}
	if calls != 2 {
		t.Fatalf("server calls = %d, want 2 (401 then retry)", calls)
	}
}

func TestCodexFailedResponseEmitsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		codexSSE(w, `{"type":"response.failed","response":{"error":{"code":"rate_limit","message":"slow down"}}}`)
	}))
	defer srv.Close()
	p, _ := NewCodex(CodexConfig{BackendURL: srv.URL, Model: "m", Tokens: &fakeTokens{access: "t"}})
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sawErr bool
	for d := range ch {
		if d.Err != nil && strings.Contains(d.Err.Error(), "slow down") {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatal("expected error delta from response.failed")
	}
}

func TestCodexRetriesOn429WithRetryAfter(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			// Retry-After: 0 skips the backoff sleep so the test is fast.
			w.Header().Set("Retry-After", "0")
			http.Error(w, `{"detail":"rate limited"}`, http.StatusTooManyRequests)
			return
		}
		codexSSE(w,
			`{"type":"response.output_text.delta","delta":"ok"}`,
			`{"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		)
	}))
	defer srv.Close()

	p, err := NewCodex(CodexConfig{BackendURL: srv.URL, Model: "gpt-5-codex", Tokens: &fakeTokens{access: "t", accountID: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var sawNotice bool
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("delta error: %v", d.Err)
		}
		if d.Notice != "" {
			sawNotice = true
		}
		text.WriteString(d.Content)
	}
	if text.String() != "ok" {
		t.Fatalf("text = %q, want ok", text.String())
	}
	if calls != 2 {
		t.Fatalf("server calls = %d, want 2 (429 then retry)", calls)
	}
	if !sawNotice {
		t.Fatal("expected a rate-limit retry Notice delta")
	}
}

func TestCodexExhausts429WithHint(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "0")
		http.Error(w, `{"detail":"rate limited"}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p, err := NewCodex(CodexConfig{BackendURL: srv.URL, Model: "gpt-5-codex", Tokens: &fakeTokens{access: "t"}})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var errStr string
	for d := range ch {
		if d.Err != nil {
			errStr = d.Err.Error()
		}
	}
	if !strings.Contains(errStr, "rate-limited") || !strings.Contains(errStr, "/model") {
		t.Fatalf("terminal error missing switch-model hint: %q", errStr)
	}
	if calls != 3 {
		t.Fatalf("server calls = %d, want 3 (maxRateLimitAttempts)", calls)
	}
}

func TestNewCodexValidation(t *testing.T) {
	if _, err := NewCodex(CodexConfig{Model: "", Tokens: &fakeTokens{}}); err == nil {
		t.Fatal("empty model should error")
	}
	if _, err := NewCodex(CodexConfig{Model: "m"}); err == nil {
		t.Fatal("nil token source should error")
	}
}
