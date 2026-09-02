package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- helpers ---

// newTestServer returns an httptest.Server that echoes the request
// body to reqBody, and uses responder to craft the response.
func newTestServer(t *testing.T, responder http.HandlerFunc) (*httptest.Server, *string) {
	t.Helper()
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured = string(body)
		responder(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

// sseResponse writes a single SSE chunk followed by [DONE].
func sseResponse(w http.ResponseWriter, chunks ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)
	for _, c := range chunks {
		_, _ = io.WriteString(w, "data: "+c+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// drainDeltas reads the entire channel and returns the deltas.
func drainDeltas(t *testing.T, ch <-chan Delta) []Delta {
	t.Helper()
	var out []Delta
	for d := range ch {
		out = append(out, d)
	}
	return out
}

// --- build request tests ---

func TestBuildOpenAIRequest_TextOnly(t *testing.T) {
	body, err := buildOpenAIRequest("gpt-4o-mini", []Message{
		{Role: RoleSystem, Content: "you are helpful"},
		{Role: RoleUser, Content: "hi"},
	}, nil, false, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Model != "gpt-4o-mini" {
		t.Fatalf("model = %q", got.Model)
	}
	if !got.Stream {
		t.Fatal("stream should be true")
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages = %d", len(got.Messages))
	}
	if got.Messages[0].Role != "system" {
		t.Fatalf("first role = %q", got.Messages[0].Role)
	}
	if got.Messages[1].Role != "user" {
		t.Fatalf("second role = %q", got.Messages[1].Role)
	}
	var sysContent string
	if err := json.Unmarshal(got.Messages[0].Content, &sysContent); err != nil {
		t.Fatalf("text-only content should be JSON string, got %s", got.Messages[0].Content)
	}
	if sysContent != "you are helpful" {
		t.Fatalf("system content = %q", sysContent)
	}
}

func TestBuildOpenAIRequest_SanitizesInvalidHistoricalToolArguments(t *testing.T) {
	body, err := buildOpenAIRequest("gpt-4o-mini", []Message{
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_bad", Name: "read_file", Arguments: `not json`}}},
		{Role: RoleTool, ToolCallID: "call_bad", Content: "invalid tool call"},
	}, nil, false, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got struct {
		Messages []struct {
			ToolCalls []struct {
				Function struct {
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Messages) == 0 || len(got.Messages[0].ToolCalls) != 1 {
		t.Fatalf("missing tool call in request: %s", body)
	}
	if args := got.Messages[0].ToolCalls[0].Function.Arguments; args != `{}` {
		t.Fatalf("arguments = %q, want {}", args)
	}
}

func TestOpenAICompleteSendsConfiguredMaxTokens(t *testing.T) {
	srv, captured := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		sseResponse(w, `{"choices":[{"delta":{"content":"ok"}}]}`)
	})
	p, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "qwen-local", MaxTokens: 321})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	drainDeltas(t, ch)
	var body struct {
		MaxTokens int `json:"max_tokens"`
	}
	if err := json.Unmarshal([]byte(*captured), &body); err != nil {
		t.Fatalf("request JSON: %v", err)
	}
	if body.MaxTokens != 321 {
		t.Fatalf("max_tokens=%d, want 321; body=%s", body.MaxTokens, *captured)
	}
}

func TestOpenAICompleteUsesPortableToolSchemaOnFirstRequest(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		var request struct {
			Tools []struct {
				Function struct {
					Parameters map[string]any `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(request.Tools) == 0 {
			t.Error("request did not contain tools")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, hasAnyOf := request.Tools[0].Function.Parameters["anyOf"]; hasAnyOf {
			t.Errorf("root anyOf reached provider: %v", request.Tools[0].Function.Parameters)
		}
		sseResponse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	p, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "grok-4.5"})
	if err != nil {
		t.Fatal(err)
	}
	tools := []ToolDef{{
		Name: "ask_user",
		Schema: `{"type":"object","properties":{"question":{"type":"string"},"options":{"type":"array"}},` +
			`"anyOf":[{"required":["question","options"]},{"required":["questions"]}]}`,
	}}

	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, tools)
	if err != nil {
		t.Fatal(err)
	}
	deltas := drainDeltas(t, ch)
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want exactly one request", got)
	}
	var content strings.Builder
	for _, delta := range deltas {
		content.WriteString(delta.Content)
		if delta.Err != nil {
			t.Fatalf("unexpected error: %v", delta.Err)
		}
	}
	if content.String() != "ok" {
		t.Fatalf("content=%q, want ok", content.String())
	}
}

// TestBuildOpenAIRequest_DemotesMidConversationSystemMessages guards the
// KV-cache fix: a system message appearing after the first non-system turn
// (freshness stamp, reflection checkpoint, ...) must NOT be hoisted into the
// leading system block — hoisting put volatile bytes at the front of the
// prompt and invalidated the whole server-side prompt cache on every minute
// tick. It stays in place, rendered as a <system-reminder> user turn.
func TestBuildOpenAIRequest_DemotesMidConversationSystemMessages(t *testing.T) {
	body, err := buildOpenAIRequest("qwen-local", []Message{
		{Role: RoleSystem, Content: "base system"},
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "hello"},
		{Role: RoleSystem, Content: "freshness stamp"},
		{Role: RoleUser, Content: "continue"},
	}, nil, false, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Messages) != 4 {
		t.Fatalf("messages = %d, want 4: %s", len(got.Messages), body)
	}
	if got.Messages[0].Role != "system" {
		t.Fatalf("first role = %q, want system", got.Messages[0].Role)
	}
	// The volatile stamp must NOT be hoisted to the prompt front.
	if !strings.Contains(got.Messages[0].Content, "base system") {
		t.Fatalf("leading system content = %q, want base system", got.Messages[0].Content)
	}
	if strings.Contains(got.Messages[0].Content, "freshness stamp") {
		t.Fatalf("volatile stamp hoisted into leading system block (cache killer): %q", got.Messages[0].Content)
	}
	for i := 1; i < len(got.Messages); i++ {
		if got.Messages[i].Role == "system" {
			t.Fatalf("message %d is system after beginning: %+v", i, got.Messages)
		}
	}
	if got.Messages[1].Role != "user" || got.Messages[2].Role != "assistant" || got.Messages[3].Role != "user" {
		t.Fatalf("non-system order changed: %+v", got.Messages)
	}
	// The stamp is rendered in place as a <system-reminder>, folded
	// together with the adjacent user turn (no user,user for strict
	// alternating templates).
	tail := got.Messages[3].Content
	if !strings.Contains(tail, "<system-reminder>\nfreshness stamp\n</system-reminder>") {
		t.Fatalf("demoted stamp missing from tail user turn: %q", tail)
	}
	if !strings.Contains(tail, "continue") {
		t.Fatalf("adjacent user text lost in merge: %q", tail)
	}
}

// TestBuildOpenAIRequest_TrailingStampStaysLast mirrors the coordinator
// shape: history ending in a tool result, then the volatile stamp. The stamp
// must come out as the LAST message, not at the front.
func TestBuildOpenAIRequest_TrailingStampStaysLast(t *testing.T) {
	body, err := buildOpenAIRequest("qwen-local", []Message{
		{Role: RoleSystem, Content: "base system"},
		{Role: RoleUser, Content: "task"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "1", Name: "list_dir", Arguments: `{"path":"."}`}}},
		{Role: RoleTool, ToolCallID: "1", Content: "a.go"},
		{Role: RoleSystem, Content: "Current local date/time: 2026-07-05 12:51"},
	}, nil, false, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Messages) != 5 {
		t.Fatalf("messages = %d, want 5: %s", len(got.Messages), body)
	}
	if strings.Contains(string(got.Messages[0].Content), "12:51") {
		t.Fatalf("stamp hoisted to leading system block: %s", got.Messages[0].Content)
	}
	last := got.Messages[len(got.Messages)-1]
	if last.Role != "user" {
		t.Fatalf("last role = %q, want user (demoted stamp)", last.Role)
	}
	if !strings.Contains(string(last.Content), "system-reminder") || !strings.Contains(string(last.Content), "12:51") {
		t.Fatalf("last message is not the demoted stamp: %s", last.Content)
	}
}

func TestBuildOpenAIRequest_Vision(t *testing.T) {
	body, err := buildOpenAIRequest("gpt-4o", []Message{
		{Role: RoleUser, Parts: []ContentPart{
			{Type: PartTypeText, Text: "what is this?"},
			{Type: PartTypeImage, Image: &ImageRef{Data: "AAAA", MediaType: "image/png"}},
		}},
	}, nil, true, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got struct {
		Messages []struct {
			Content []openaiPart `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	parts := got.Messages[0].Content
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "what is this?" {
		t.Fatalf("text part wrong: %+v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil {
		t.Fatalf("image part wrong: %+v", parts[1])
	}
	want := "data:image/png;base64,AAAA"
	if parts[1].ImageURL.URL != want {
		t.Fatalf("image url = %q, want %q", parts[1].ImageURL.URL, want)
	}
}

func TestBuildOpenAIRequest_VisionDisabledReplacesImages(t *testing.T) {
	body, err := buildOpenAIRequest("gpt-3.5-turbo", []Message{
		{Role: RoleUser, Parts: []ContentPart{
			{Type: PartTypeText, Text: "hi"},
			{Type: PartTypeImage, Image: &ImageRef{Data: "AAAA", MediaType: "image/png"}},
		}},
	}, nil, false, false) // vision = false
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var content string
	if err := json.Unmarshal(got.Messages[0].Content, &content); err != nil {
		t.Fatalf("vision-disabled text should be JSON string, got %s", got.Messages[0].Content)
	}
	want := "hi\n" + imageInputOmittedPlaceholder
	if content != want {
		t.Fatalf("content = %q, want %q", content, want)
	}
}

func TestBuildOpenAIRequest_FileBackedImageLoadsOnlyForVision(t *testing.T) {
	path := t.TempDir() + string(os.PathSeparator) + "tool.png"
	if err := os.WriteFile(path, []byte("tool-image"), 0o600); err != nil {
		t.Fatal(err)
	}
	msg := Message{Role: RoleUser, Parts: []ContentPart{
		{Type: PartTypeText, Text: "look"},
		{Type: PartTypeImage, Image: &ImageRef{Path: path, MediaType: "image/png"}},
	}}

	body, err := buildOpenAIRequest("vision-model", []Message{msg}, nil, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "dG9vbC1pbWFnZQ==") {
		t.Fatalf("file-backed image was not loaded at transport boundary: %s", body)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	body, err = buildOpenAIRequest("text-model", []Message{msg}, nil, false, false)
	if err != nil {
		t.Fatalf("text-only request should not touch missing image file: %v", err)
	}
	if strings.Contains(string(body), "image_url") || !strings.Contains(string(body), imageInputOmittedPlaceholder) {
		t.Fatalf("text-only request did not replace file-backed image: %s", body)
	}
}

func TestBuildOpenAIRequest_ImageURLPassthrough(t *testing.T) {
	body, err := buildOpenAIRequest("gpt-4o", []Message{
		{Role: RoleUser, Parts: []ContentPart{
			{Type: PartTypeImage, Image: &ImageRef{URL: "https://example.com/cat.png"}},
		}},
	}, nil, true, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got struct {
		Messages []struct {
			Content []openaiPart `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(body, &got)
	if got.Messages[0].Content[0].ImageURL.URL != "https://example.com/cat.png" {
		t.Fatalf("url not preserved: %+v", got.Messages[0].Content[0])
	}
}

func TestBuildOpenAIRequest_EmptyMessagesFails(t *testing.T) {
	// buildOpenAIRequest itself is a pure builder and accepts an
	// empty slice. JSON marshals a nil slice as "null"; the empty
	// check lives in Complete. This test documents the behaviour.
	body, err := buildOpenAIRequest("gpt-4o", nil, nil, false, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(string(body), `"messages":null`) {
		t.Fatalf("body should contain null messages for nil input, got %s", body)
	}
}

// --- streaming tests ---

func TestOpenAI_StreamText(t *testing.T) {
	chunks := []string{
		`{"id":"1","object":"chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":""}]}`,
		`{"id":"1","object":"chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":""}]}`,
		`{"id":"1","object":"chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":""}]}`,
		`{"id":"1","object":"chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	srv, captured := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing auth: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("missing sse accept: %q", r.Header.Get("Accept"))
		}
		sseResponse(w, chunks...)
	})

	p, _ := NewOpenAI(OpenAIConfig{
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Model:   "gpt-4o",
	})
	ch, err := p.Complete(context.Background(), []Message{
		{Role: RoleUser, Content: "hi"},
	}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds := drainDeltas(t, ch)
	if len(ds) < 4 {
		t.Fatalf("deltas = %d, want >= 4", len(ds))
	}
	// First delta is the role.
	if ds[0].Role != RoleAssistant {
		t.Fatalf("first delta role = %q", ds[0].Role)
	}
	// Concatenate content.
	var body strings.Builder
	for _, d := range ds {
		body.WriteString(d.Content)
	}
	if body.String() != "Hello world" {
		t.Fatalf("body = %q, want Hello world", body.String())
	}
	// Last delta has finish_reason.
	last := ds[len(ds)-1]
	if last.FinishReason != "stop" {
		t.Fatalf("last finish = %q, want stop", last.FinishReason)
	}
	// Request body sanity.
	if !strings.Contains(*captured, `"stream":true`) {
		t.Fatalf("request missing stream:true: %s", *captured)
	}
	if !strings.Contains(*captured, `"model":"gpt-4o"`) {
		t.Fatalf("request missing model: %s", *captured)
	}
}

func TestOpenAI_CleansPastedAPIKeyForChatCompletions(t *testing.T) {
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("auth = %q, want Bearer sk-test", got)
		}
		sseResponse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
	})
	p, err := NewOpenAI(OpenAIConfig{
		BaseURL: srv.URL,
		APIKey:  " sk-\r\ntest\t ",
		Model:   "gpt-4o",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.cfg.APIKey != "sk-test" {
		t.Fatalf("stored APIKey = %q, want sk-test", p.cfg.APIKey)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = drainDeltas(t, ch)
}

func TestOpenAI_StreamStopsOnDoneEvenIfServerKeepsConnectionOpen(t *testing.T) {
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"Cześć"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		for _, c := range chunks {
			_, _ = io.WriteString(w, "data: "+c+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		// Simulate LM Studio / OpenAI-compatible servers that don't
		// immediately close the HTTP connection after [DONE]. The
		// client must stop reading as soon as [DONE] arrives.
		time.Sleep(500 * time.Millisecond)
	})
	defer srv.Close()

	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "qwen-local"})
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "cześć"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	done := make(chan []Delta, 1)
	go func() { done <- drainDeltas(t, ch) }()

	select {
	case ds := <-done:
		var body strings.Builder
		for _, d := range ds {
			body.WriteString(d.Content)
		}
		if body.String() != "Cześć" {
			t.Fatalf("body = %q, want Cześć", body.String())
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("stream did not finish promptly after [DONE]")
	}
}

func TestOpenAI_StreamParsesDataLinesWithoutBlankSeparators(t *testing.T) {
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"Hej"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"!"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		// LM Studio-like broken-ish SSE: each data line arrives
		// without an empty line separator. Client must still parse
		// every data: payload independently.
		for _, c := range chunks {
			_, _ = io.WriteString(w, "data: "+c+"\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n")
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(500 * time.Millisecond)
	})
	defer srv.Close()

	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "qwen-local"})
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "cześć"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	done := make(chan []Delta, 1)
	go func() { done <- drainDeltas(t, ch) }()

	select {
	case ds := <-done:
		var body strings.Builder
		for _, d := range ds {
			body.WriteString(d.Content)
		}
		if body.String() != "Hej!" {
			t.Fatalf("body = %q, want Hej!", body.String())
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("stream did not parse line-based data chunks promptly")
	}
}

func TestOpenAI_StreamReasoningContentIsShown(t *testing.T) {
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Thinking"}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":" Process"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"\n\nCześć!"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, chunks...)
	})
	defer srv.Close()

	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "qwen-local"})
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "cześć"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds := drainDeltas(t, ch)
	var reasoning, body strings.Builder
	for _, d := range ds {
		reasoning.WriteString(d.Reasoning)
		body.WriteString(d.Content)
	}
	if reasoning.String() != "Thinking Process" || body.String() != "\n\nCześć!" {
		t.Fatalf("reasoning=%q body=%q, want separate reasoning + answer", reasoning.String(), body.String())
	}
}

func TestOpenAI_StreamGenericThinkingFieldsAreShown(t *testing.T) {
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","thinking":"Plan"}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning":{"content":" A"}}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"\nAnswer"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, chunks...)
	})
	defer srv.Close()

	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "deepseek-reasoner"})
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "cześć"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds := drainDeltas(t, ch)
	var reasoning, body strings.Builder
	for _, d := range ds {
		reasoning.WriteString(d.Reasoning)
		body.WriteString(d.Content)
	}
	if reasoning.String() != "Plan A" || body.String() != "\nAnswer" {
		t.Fatalf("reasoning=%q body=%q, want generic thinking + answer", reasoning.String(), body.String())
	}
}

func TestOpenAI_StreamStructuredReasoningMetadataNotShown(t *testing.T) {
	// Newer llama.cpp builds stream reasoning as a structured object
	// whose metadata fields (type/format) must never leak into the
	// visible text. Regression: the GUI showed "Let me
	// checkreasoning.textunknown …".
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","reasoning":{"type":"reasoning.text","format":"unknown","text":"Let me check"}}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning":{"type":"reasoning.text","format":"unknown","text":" the file"}}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"Answer"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, chunks...)
	})
	defer srv.Close()

	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "qwen-local"})
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds := drainDeltas(t, ch)
	var reasoning, body strings.Builder
	for _, d := range ds {
		reasoning.WriteString(d.Reasoning)
		body.WriteString(d.Content)
	}
	if reasoning.String() != "Let me check the file" || body.String() != "Answer" {
		t.Fatalf("reasoning=%q body=%q, want clean reasoning without metadata leakage", reasoning.String(), body.String())
	}
}

func TestOpenAI_StreamAnalysisFieldIsShownAsThinking(t *testing.T) {
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","analysis":"Check"}}]}`,
		`{"choices":[{"index":0,"delta":{"analysis_content":" this carefully"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"Answer"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, chunks...)
	})
	defer srv.Close()

	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "reasoning-gateway-model"})
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds := drainDeltas(t, ch)
	var reasoning, body strings.Builder
	for _, d := range ds {
		reasoning.WriteString(d.Reasoning)
		body.WriteString(d.Content)
	}
	if reasoning.String() != "Check this carefully" || body.String() != "Answer" {
		t.Fatalf("reasoning=%q body=%q, want analysis stream rendered separately", reasoning.String(), body.String())
	}
}

func TestExtractReasoningText_MirroredKeysNotDoubled(t *testing.T) {
	// One reasoning delta mirrored under two keys must yield the text
	// once, not word-doubled.
	delta := map[string]json.RawMessage{
		"reasoning":      json.RawMessage(`{"type":"reasoning.text","text":"what"}`),
		"reasoning_text": json.RawMessage(`"what"`),
	}
	if got := extractReasoningText(delta); got != "what" {
		t.Fatalf("extractReasoningText = %q, want %q", got, "what")
	}
}

func TestOpenAI_StreamToolCallsNotDuplicatedOnDoubleFinish(t *testing.T) {
	// Some servers send finish_reason twice (the tool_calls finish plus
	// a final usage frame). The accumulated tool calls must be emitted
	// once — a re-flush made the agent loop execute every call twice.
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"list_dir","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, chunks...)
	})
	defer srv.Close()

	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "qwen-local"})
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "ls"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds := drainDeltas(t, ch)
	calls := 0
	for _, d := range ds {
		if d.ToolCall != nil {
			calls++
		}
	}
	if calls != 1 {
		t.Fatalf("tool call deltas = %d, want exactly 1", calls)
	}
}

func TestExtractReasoningTextIgnoresUsageLikeFields(t *testing.T) {
	delta := map[string]json.RawMessage{
		"reasoning_tokens": json.RawMessage(`123`),
		"finish_reason":    json.RawMessage(`"stop"`),
		"thinking_text":    json.RawMessage(`"visible"`),
	}
	if got := extractReasoningText(delta); got != "visible" {
		t.Fatalf("extractReasoningText = %q, want visible", got)
	}
}

func TestOpenAI_StreamToolCall(t *testing.T) {
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_image","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"foo.png\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, chunks...)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Model: "gpt-4o"})
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "look at foo.png"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds := drainDeltas(t, ch)
	// Find the tool call delta.
	var tc *ToolCall
	for _, d := range ds {
		if d.ToolCall != nil {
			tc = d.ToolCall
		}
	}
	if tc == nil {
		t.Fatal("no tool call delta")
	}
	if tc.ID != "call_1" {
		t.Fatalf("id = %q", tc.ID)
	}
	if tc.Name != "read_image" {
		t.Fatalf("name = %q", tc.Name)
	}
	if tc.Arguments != `{"path":"foo.png"}` {
		t.Fatalf("args = %q", tc.Arguments)
	}
	last := ds[len(ds)-1]
	if last.FinishReason != "tool_calls" {
		t.Fatalf("finish = %q", last.FinishReason)
	}
}

func TestOpenAI_StreamUsage(t *testing.T) {
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, chunks...)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Model: "gpt-4o-mini"})
	ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	ds := drainDeltas(t, ch)
	last := ds[len(ds)-1]
	if last.Usage == nil {
		t.Fatal("expected usage on last delta")
	}
	if last.Usage.Input != 5 || last.Usage.Output != 2 || last.Usage.Total != 7 {
		t.Fatalf("usage = %+v", last.Usage)
	}
}

// TestOpenAI_StreamUsageInSeparateEmptyChoicesChunk covers the
// real shape LM Studio / vLLM / OpenAI emit when
// stream_options.include_usage is set: the usage arrives in a
// FINAL chunk whose choices array is EMPTY. A regression here
// makes every streamed run report 0 tokens.
func TestOpenAI_StreamUsageInSeparateEmptyChoicesChunk(t *testing.T) {
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		// Separate trailing usage-only chunk, no choices.
		`{"choices":[],"usage":{"prompt_tokens":409,"completion_tokens":61,"total_tokens":470}}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, chunks...)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Model: "gpt-4o-mini"})
	ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	ds := drainDeltas(t, ch)

	var usage *Usage
	for _, d := range ds {
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	if usage == nil {
		t.Fatal("expected usage from the empty-choices trailing chunk")
	}
	if usage.Input != 409 || usage.Output != 61 || usage.Total != 470 {
		t.Fatalf("usage = %+v, want 409/61/470", usage)
	}
}

// TestOpenAI_StreamUsageParsesDetails covers the cached-prompt and
// reasoning-token breakdown LM Studio / OpenAI report inside usage:
// prompt_tokens_details.cached_tokens and
// completion_tokens_details.reasoning_tokens must reach llm.Usage so
// the status line can show cache-hit% and think tokens.
func TestOpenAI_StreamUsageParsesDetails(t *testing.T) {
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":1800,"completion_tokens":502,"total_tokens":2302,"prompt_tokens_details":{"cached_tokens":1656},"completion_tokens_details":{"reasoning_tokens":496}}}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, chunks...)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Model: "qwen3.5-9b"})
	ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	ds := drainDeltas(t, ch)

	var usage *Usage
	for _, d := range ds {
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	if usage == nil {
		t.Fatal("expected usage")
	}
	if usage.Input != 1800 || usage.Output != 502 {
		t.Fatalf("usage = %+v, want 1800 in / 502 out", usage)
	}
	if usage.CachedInput != 1656 {
		t.Fatalf("CachedInput = %d, want 1656", usage.CachedInput)
	}
	if usage.Reasoning != 496 {
		t.Fatalf("Reasoning = %d, want 496", usage.Reasoning)
	}
}

// TestOpenAI_StreamUsageNoDetailsZeroValues proves that usage without
// the details objects (most cloud backends) yields zero cached/reasoning
// without panicking on the nil detail pointers.
func TestOpenAI_StreamUsageNoDetailsZeroValues(t *testing.T) {
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, chunks...)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Model: "gpt-4o-mini"})
	ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	ds := drainDeltas(t, ch)

	var usage *Usage
	for _, d := range ds {
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	if usage == nil {
		t.Fatal("expected usage")
	}
	if usage.CachedInput != 0 || usage.Reasoning != 0 {
		t.Fatalf("want zero details, got Cached=%d Reasoning=%d", usage.CachedInput, usage.Reasoning)
	}
}

// TestOpenAI_LlamaTimingsDeriveCachedInput covers llama.cpp servers
// whose usage carries no prompt_tokens_details: the timings block
// (cache_n = prompt tokens reused from the KV cache, prompt_n =
// prompt tokens re-evaluated) must fill Usage.CachedInput so the
// per-turn cache-miss line works on local backends too.
func TestOpenAI_LlamaTimingsDeriveCachedInput(t *testing.T) {
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":14580,"completion_tokens":220,"total_tokens":14800},"timings":{"prompt_n":380,"cache_n":14200,"predicted_n":220,"prompt_ms":812.5,"predicted_per_second":31.2}}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, chunks...)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "qwen3.5-9b"})
	ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	ds := drainDeltas(t, ch)

	var usage *Usage
	for _, d := range ds {
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	if usage == nil {
		t.Fatal("expected usage")
	}
	if usage.Input != 14580 || usage.Output != 220 {
		t.Fatalf("usage = %+v, want 14580 in / 220 out", usage)
	}
	if usage.CachedInput != 14200 {
		t.Fatalf("CachedInput = %d, want 14200 (from timings.cache_n)", usage.CachedInput)
	}
}

// TestOpenAI_LlamaTimingsWithoutCacheN covers older llama.cpp builds
// whose timings lack cache_n: the cached share is derived as
// prompt_tokens - prompt_n (everything the server did not re-evaluate
// came from the cache). The timings here ride the finish chunk while
// usage arrives in the later empty-choices chunk, exercising the
// cross-chunk memory.
func TestOpenAI_LlamaTimingsWithoutCacheN(t *testing.T) {
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"timings":{"prompt_n":380,"predicted_n":220}}`,
		`{"choices":[],"usage":{"prompt_tokens":14580,"completion_tokens":220,"total_tokens":14800}}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, chunks...)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "qwen3.5-9b"})
	ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	ds := drainDeltas(t, ch)

	var usage *Usage
	for _, d := range ds {
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	if usage == nil {
		t.Fatal("expected usage")
	}
	if usage.CachedInput != 14200 {
		t.Fatalf("CachedInput = %d, want 14200 (prompt_tokens - prompt_n)", usage.CachedInput)
	}
}

// TestOpenAI_UsageDetailsWinOverTimings: when the server reports BOTH
// prompt_tokens_details.cached_tokens and a timings block (newer
// llama.cpp does), the OpenAI-style details are authoritative and the
// timings derivation must not overwrite them.
func TestOpenAI_UsageDetailsWinOverTimings(t *testing.T) {
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":2000,"completion_tokens":50,"total_tokens":2050,"prompt_tokens_details":{"cached_tokens":1000}},"timings":{"prompt_n":500,"cache_n":999}}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, chunks...)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "qwen3.5-9b"})
	ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	ds := drainDeltas(t, ch)

	var usage *Usage
	for _, d := range ds {
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	if usage == nil {
		t.Fatal("expected usage")
	}
	if usage.CachedInput != 1000 {
		t.Fatalf("CachedInput = %d, want 1000 (details win over timings)", usage.CachedInput)
	}
}

// TestOpenAI_RequestIncludesStreamOptions verifies the request
// asks for usage in streaming mode; without it servers stay silent.
func TestOpenAI_RequestIncludesStreamOptions(t *testing.T) {
	body, err := buildOpenAIRequest("gpt-4o-mini", []Message{{Role: RoleUser, Content: "hi"}}, nil, false, false)
	if err != nil {
		t.Fatalf("buildOpenAIRequest: %v", err)
	}
	if !strings.Contains(string(body), `"stream_options"`) {
		t.Errorf("request missing stream_options: %s", body)
	}
	if !strings.Contains(string(body), `"include_usage":true`) {
		t.Errorf("request missing include_usage: %s", body)
	}
}

func TestOpenAI_StreamMultimodalRequest(t *testing.T) {
	srv, captured := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w,
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":"a cat"}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		)
	})
	// F16: pass an explicit registry so the
	// provider knows gpt-4o has vision. Tests that
	// need specific capabilities must wire the
	// registry themselves.
	caps := NewCapabilityRegistry()
	caps.Register(ModelInfo{ID: "gpt-4o", Vision: true, ToolUse: true, Stream: true, Source: SourceSeed})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Model: "gpt-4o", Capabilities: caps})
	ch, err := p.Complete(context.Background(), []Message{{
		Role: RoleUser,
		Parts: []ContentPart{
			{Type: PartTypeText, Text: "what is this?"},
			{Type: PartTypeImage, Image: &ImageRef{Data: "BASE64DATA", MediaType: "image/jpeg"}},
		},
	}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// Drain the channel so the HTTP request is guaranteed to be done.
	drainDeltas(t, ch)
	if !strings.Contains(*captured, `"image_url"`) {
		t.Fatalf("request missing image_url: %s", *captured)
	}
	if !strings.Contains(*captured, "data:image/jpeg;base64,BASE64DATA") {
		t.Fatalf("request missing data URI: %s", *captured)
	}
}

func TestOpenAI_HTTPError(t *testing.T) {
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key"}}`)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "bad", Model: "gpt-4o"})
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete should not return error upfront: %v", err)
	}
	ds := drainDeltas(t, ch)
	if len(ds) != 1 || ds[0].Err == nil {
		t.Fatalf("expected single error delta, got %+v", ds)
	}
	if !strings.Contains(ds[0].Err.Error(), "401") {
		t.Fatalf("err = %v", ds[0].Err)
	}
}

func TestOpenAI_NetworkError(t *testing.T) {
	// Point at a port that's not listening.
	p, _ := NewOpenAI(OpenAIConfig{
		BaseURL:    "http://127.0.0.1:1",
		APIKey:     "k",
		Model:      "gpt-4o",
		HTTPClient: &http.Client{Timeout: 500 * time.Millisecond},
	})
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds := drainDeltas(t, ch)
	if len(ds) != 1 || ds[0].Err == nil {
		t.Fatalf("expected error delta, got %+v", ds)
	}
}

func TestOpenAI_ContextCancel(t *testing.T) {
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		flusher.Flush()
		// Hang forever; the test will cancel.
		<-r.Context().Done()
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Model: "gpt-4o"})
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	<-ch // first delta
	cancel()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, open := <-ch:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("channel did not close after cancel")
		}
	}
}

func TestOpenAI_EmptyMessages(t *testing.T) {
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: "http://x", APIKey: "k", Model: "gpt-4o"})
	if _, err := p.Complete(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error on empty messages")
	}
}

func TestOpenAI_InvalidMessage(t *testing.T) {
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: "http://x", APIKey: "k", Model: "gpt-4o"})
	_, err := p.Complete(context.Background(), []Message{{Role: Role("wizard"), Content: "x"}}, nil)
	if err == nil {
		t.Fatal("expected error on invalid role")
	}
}

func TestNewOpenAI_Validation(t *testing.T) {
	// Empty API key is allowed (local providers like LM Studio don't need one).
	t.Run("no key allowed", func(t *testing.T) {
		p, err := NewOpenAI(OpenAIConfig{Model: "gpt-4o"})
		if err != nil {
			t.Fatalf("NewOpenAI with empty key should succeed: %v", err)
		}
		if p.Name() != "gpt-4o" {
			t.Fatalf("Name = %q, want gpt-4o", p.Name())
		}
	})
	// Empty model is still rejected.
	t.Run("no model", func(t *testing.T) {
		if _, err := NewOpenAI(OpenAIConfig{APIKey: "k"}); err == nil {
			t.Fatal("expected validation error for empty model")
		}
	})
}

func TestNewOpenAI_DefaultsBaseURL(t *testing.T) {
	p, err := NewOpenAI(OpenAIConfig{APIKey: "k", Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	if !strings.HasPrefix(p.cfg.BaseURL, "https://") {
		t.Fatalf("default base URL = %q", p.cfg.BaseURL)
	}
}

func TestOpenAI_Name(t *testing.T) {
	p, _ := NewOpenAI(OpenAIConfig{APIKey: "k", Model: "gpt-4o-mini"})
	if p.Name() != "gpt-4o-mini" {
		t.Fatalf("Name = %q", p.Name())
	}
}

func TestOpenAI_SupportsVision(t *testing.T) {
	// F16: providers no longer auto-seed a builtin
	// table; the test passes an explicit registry
	// populated from the seed.
	caps := NewCapabilityRegistry()
	if seed, err := LoadSeed(); err == nil {
		caps.RegisterAll(seed)
	} else {
		// Fallback for the test environment — at
		// minimum register the model the test asks
		// about, so the test still works if the
		// embed somehow fails.
		caps.Register(ModelInfo{ID: "gpt-4o", Vision: true, ToolUse: true, Stream: true, Source: SourceSeed})
		caps.Register(ModelInfo{ID: "gpt-3.5-turbo", Vision: false, ToolUse: true, Stream: true, Source: SourceSeed})
	}
	p1, _ := NewOpenAI(OpenAIConfig{APIKey: "k", Model: "gpt-4o", Capabilities: caps})
	if !p1.SupportsVision() {
		t.Fatal("gpt-4o should support vision")
	}
	p2, _ := NewOpenAI(OpenAIConfig{APIKey: "k", Model: "gpt-3.5-turbo", Capabilities: caps})
	if p2.SupportsVision() {
		t.Fatal("gpt-3.5-turbo should NOT support vision")
	}
	p3, _ := NewOpenAI(OpenAIConfig{APIKey: "k", Model: "unknown-model-xyz", Capabilities: caps})
	if p3.SupportsVision() {
		t.Fatal("unknown model should default to no vision")
	}
}

func TestOpenAI_UnknownCapabilityStillAttemptsImage(t *testing.T) {
	srv, body := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, `{"choices":[{"delta":{"content":"hi"}}]}`)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Model: "gpt-3.5-turbo"})
	ch, err := p.Complete(context.Background(), []Message{{
		Role: RoleUser,
		Parts: []ContentPart{
			{Type: PartTypeText, Text: "look"},
			{Type: PartTypeImage, Image: &ImageRef{Data: "AAA", MediaType: "image/png"}},
		},
	}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds := drainDeltas(t, ch)
	for _, delta := range ds {
		if delta.Err != nil {
			t.Fatalf("unexpected local capability error: %v", delta.Err)
		}
	}
	if !strings.Contains(*body, "image_url") || !strings.Contains(*body, "data:image/png;base64,AAA") {
		t.Fatalf("image was not forwarded to provider: %s", *body)
	}
}

func TestOpenAI_KnownTextOnlyCapabilityReplacesImage(t *testing.T) {
	srv, body := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, `{"choices":[{"delta":{"content":"hi"}}]}`)
	})
	caps := NewCapabilityRegistry()
	caps.Register(ModelInfo{ID: "text-only-model", Vision: false, VisionKnown: true, Source: SourceProvider})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Model: "text-only-model", Capabilities: caps})
	ch, err := p.Complete(context.Background(), []Message{{
		Role: RoleUser,
		Parts: []ContentPart{
			{Type: PartTypeText, Text: "look"},
			{Type: PartTypeImage, Image: &ImageRef{Data: "AAA", MediaType: "image/png"}},
		},
	}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	for _, delta := range drainDeltas(t, ch) {
		if delta.Err != nil {
			t.Fatalf("unexpected error: %v", delta.Err)
		}
	}
	if strings.Contains(*body, "image_url") || strings.Contains(*body, "data:image/png;base64,AAA") {
		t.Fatalf("known text-only model received image bytes: %s", *body)
	}
	if !strings.Contains(*body, imageInputOmittedPlaceholder) {
		t.Fatalf("request should preserve an image placeholder: %s", *body)
	}
}

// TestOpenAI_RetryWithoutImagesOnRejection verifies the self-healing
// path using the exact nested Console/provider error seen in production:
// the provider learns the rejection, retries once without images and
// completes normally instead of surfacing HTTP 400.
func TestOpenAI_RetryWithoutImagesOnRejection(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, string(body))
		if len(requests) == 1 {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"param":null,"type":"invalid_request_error","message":"Error from provider (Console): Upstream request failed: [400] Model only supports text input; received unsupported content type 'image_url'."}}`)
			return
		}
		sseResponse(w,
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":"ok"}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		)
	}))
	t.Cleanup(srv.Close)

	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Model: "text-only-model"})
	ch, err := p.Complete(context.Background(), []Message{{
		Role: RoleUser,
		Parts: []ContentPart{
			{Type: PartTypeText, Text: "what is this?"},
			{Type: PartTypeImage, Image: &ImageRef{Data: "BASE64DATA", MediaType: "image/jpeg"}},
		},
	}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds := drainDeltas(t, ch)
	for _, d := range ds {
		if d.Err != nil {
			t.Fatalf("unexpected error delta: %v", d.Err)
		}
	}
	if len(requests) != 2 {
		t.Fatalf("got %d requests, want 2 (rejected + retried)", len(requests))
	}
	if !strings.Contains(requests[0], `"image_url"`) {
		t.Fatalf("first request should carry the image: %s", requests[0])
	}
	if strings.Contains(requests[1], `"image_url"`) {
		t.Fatalf("retry must not carry the image: %s", requests[1])
	}
	var sawNotice bool
	for _, d := range ds {
		if d.Notice != "" {
			sawNotice = true
		}
	}
	if !sawNotice {
		t.Fatal("expected a Notice delta explaining the image retry")
	}
}

func TestIsImageRejection_KnownProviderWordings(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "serde text-only schema",
			body: `{"error":{"message":"unknown variant image_url, expected text"}}`,
			want: true,
		},
		{
			name: "console wrapped text-only rejection",
			body: `{"error":{"message":"Model only supports text input; received unsupported content type 'image_url'."}}`,
			want: true,
		},
		{
			name: "malformed image remains a real error",
			body: `{"error":{"message":"image_url data is malformed"}}`,
			want: false,
		},
		{
			name: "unsupported image media type does not disable vision",
			body: `{"error":{"message":"unsupported content type image/png for image_url"}}`,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isImageRejection(http.StatusBadRequest, []byte(tt.body)); got != tt.want {
				t.Fatalf("isImageRejection() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestOpenAI_LearnedRejectionSkipsImagesOnNextTurn verifies that after a
// rejection is learned, the very next request is built without image
// parts — no doomed round-trip to the upstream.
func TestOpenAI_LearnedRejectionSkipsImagesOnNextTurn(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		sseResponse(w,
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":"ok"}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		)
	}))
	t.Cleanup(srv.Close)

	caps := NewCapabilityRegistry()
	caps.Register(ModelInfo{ID: "text-only", Source: SourceProvider})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Model: "text-only", Capabilities: caps})
	p.imageRejected.Store(true)

	// A rejection learned by this exact transport instance makes the next
	// turn skip image parts without poisoning the shared model registry.
	ch, err := p.Complete(context.Background(), []Message{{
		Role: RoleUser,
		Parts: []ContentPart{
			{Type: PartTypeText, Text: "look"},
			{Type: PartTypeImage, Image: &ImageRef{Data: "AAA", MediaType: "image/png"}},
		},
	}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	drainDeltas(t, ch)
	if len(bodies) != 1 {
		t.Fatalf("got %d requests, want 1 (learned rejection skips the doomed attempt)", len(bodies))
	}
	if strings.Contains(bodies[0], `"image_url"`) {
		t.Fatalf("learned text-only model must not receive images: %s", bodies[0])
	}
}

// TestOpenAI_NonRejectionImage400DoesNotBlindVisionModel verifies the
// safety property: a 400 that merely mentions image_url for an unrelated
// reason (malformed attachment, bad media type) is surfaced as an error
// and does NOT mark the model text-only. A vision-capable model keeps
// receiving images on the next turn.
func TestOpenAI_NonRejectionImage400DoesNotBlindVisionModel(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, string(body))
		if len(requests) == 1 {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"message":"image_url data is malformed"}}`)
			return
		}
		sseResponse(w,
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":"ok"}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		)
	}))
	t.Cleanup(srv.Close)

	caps := NewCapabilityRegistry()
	caps.Register(ModelInfo{ID: "gpt-4o", Vision: true, VisionKnown: true, Source: SourceSeed})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Model: "gpt-4o", Capabilities: caps})
	img := func() []Message {
		return []Message{{
			Role: RoleUser,
			Parts: []ContentPart{
				{Type: PartTypeText, Text: "look"},
				{Type: PartTypeImage, Image: &ImageRef{Data: "AAA", MediaType: "image/png"}},
			},
		}}
	}

	// Turn 1: the 400 is an error, not a rejection — no retry, no learning.
	ch, err := p.Complete(context.Background(), img(), nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds := drainDeltas(t, ch)
	if len(ds) != 1 || ds[0].Err == nil {
		t.Fatalf("expected a single error delta, got %+v", ds)
	}
	if len(requests) != 1 {
		t.Fatalf("got %d requests, want 1 (no image-retry for a non-rejection 400)", len(requests))
	}
	if !strings.Contains(requests[0], `"image_url"`) {
		t.Fatalf("first request should carry the image: %s", requests[0])
	}

	// Turn 2: the model was NOT learned as text-only — images still sent.
	ch, err = p.Complete(context.Background(), img(), nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds = drainDeltas(t, ch)
	for _, d := range ds {
		if d.Err != nil {
			t.Fatalf("unexpected error delta: %v", d.Err)
		}
	}
	if len(requests) != 2 || !strings.Contains(requests[1], `"image_url"`) {
		t.Fatalf("vision model must keep receiving images after an unrelated 400: %+v", requests)
	}
}

func TestEncodeBase64(t *testing.T) {
	if got := EncodeBase64([]byte("hello")); got != "aGVsbG8=" {
		t.Fatalf("EncodeBase64 = %q", got)
	}
}

// --- chunk-decoding resilience ---

func TestOpenAI_IgnoresKnownHeartbeatChunk(t *testing.T) {
	// Real providers sometimes send explicit ping frames between JSON chunks.
	chunks := []string{
		`ping`,
		`{"choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, chunks...)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Model: "gpt-4o"})
	ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil)
	ds := drainDeltas(t, ch)
	var body strings.Builder
	for _, d := range ds {
		body.WriteString(d.Content)
	}
	if body.String() != "ok" {
		t.Fatalf("body = %q, want ok", body.String())
	}
}

func TestOpenAI_MalformedChunkIsAnError(t *testing.T) {
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, `not json`)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "model"})
	ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil)
	ds := drainDeltas(t, ch)
	if len(ds) == 0 || ds[len(ds)-1].Err == nil || !strings.Contains(ds[len(ds)-1].Err.Error(), "unexpected SSE payload") {
		t.Fatalf("expected malformed SSE error, got %+v", ds)
	}
}

func TestOpenAI_StructuredStreamErrorIsAnError(t *testing.T) {
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, `{"error":{"message":"upstream overloaded","type":"server_error"}}`)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "model"})
	ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil)
	ds := drainDeltas(t, ch)
	if len(ds) == 0 || ds[len(ds)-1].Err == nil || !strings.Contains(ds[len(ds)-1].Err.Error(), "upstream overloaded") {
		t.Fatalf("expected structured provider error, got %+v", ds)
	}
}

func TestOpenAI_EmptyAndIncompleteStreams(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			sseResponse(w)
		})
		p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "model"})
		ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil)
		ds := drainDeltas(t, ch)
		if len(ds) == 0 || ds[len(ds)-1].Err == nil || !strings.Contains(ds[len(ds)-1].Err.Error(), "empty stream") {
			t.Fatalf("expected empty stream error, got %+v", ds)
		}
	})
	t.Run("clean eof after content is an implicit stop", func(t *testing.T) {
		srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		})
		p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "model"})
		ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil)
		ds := drainDeltas(t, ch)
		if len(ds) < 2 || ds[len(ds)-1].Err != nil || ds[len(ds)-1].FinishReason != "stop" {
			t.Fatalf("expected implicit stop after valid content, got %+v", ds)
		}
	})
	t.Run("clean eof flushes tool call", func(t *testing.T) {
		srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"ask-1\",\"type\":\"function\",\"function\":{\"name\":\"ask_user\",\"arguments\":\"{}\"}}]}}]}\n\n")
		})
		p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "model"})
		ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil)
		ds := drainDeltas(t, ch)
		var sawTool bool
		for _, d := range ds {
			if d.ToolCall != nil && d.ToolCall.Name == "ask_user" && d.ToolCall.ID == "ask-1" {
				sawTool = true
			}
		}
		if !sawTool || len(ds) == 0 || ds[len(ds)-1].Err != nil || ds[len(ds)-1].FinishReason != "tool_calls" {
			t.Fatalf("expected flushed tool call and implicit tool_calls finish, got %+v", ds)
		}
	})
	t.Run("metadata only eof remains an error", func(t *testing.T) {
		srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-x\",\"object\":\"chat.completion.chunk\",\"choices\":[]}\n\n")
		})
		p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "model"})
		ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil)
		ds := drainDeltas(t, ch)
		if len(ds) == 0 || ds[len(ds)-1].Err == nil || !strings.Contains(ds[len(ds)-1].Err.Error(), "terminal event") {
			t.Fatalf("expected metadata-only incomplete stream error, got %+v", ds)
		}
	})
}

// --- request counter (proves we use BaseURL correctly) ---

func TestOpenAI_RequestURL(t *testing.T) {
	var got atomic.Value
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.URL.Path)
		sseResponse(w, `{"choices":[{"delta":{"content":"x"}}]}`)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Model: "gpt-4o"})
	ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil)
	drainDeltas(t, ch) // wait for the request to be processed
	if got.Load() != "/v1/chat/completions" {
		t.Fatalf("path = %v, want /v1/chat/completions", got.Load())
	}
}

func TestOpenAI_MultipleToolCalls(t *testing.T) {
	// Two tool calls arriving in parallel deltas.
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"a","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"c2","type":"function","function":{"name":"b","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, chunks...)
	})
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Model: "gpt-4o"})
	ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "do"}}, nil)
	ds := drainDeltas(t, ch)
	calls := map[string]bool{}
	for _, d := range ds {
		if d.ToolCall != nil {
			calls[d.ToolCall.Name] = true
		}
	}
	if !calls["a"] || !calls["b"] {
		t.Fatalf("missing tool calls: %+v", calls)
	}
}

func TestOpenAI_SparseToolCallIndexIsFlushed(t *testing.T) {
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":3,"id":"c3","type":"function","function":{"name":"third","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) { sseResponse(w, chunks...) })
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "model"})
	ch, _ := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "do"}}, nil)
	ds := drainDeltas(t, ch)
	for _, d := range ds {
		if d.ToolCall != nil && d.ToolCall.Name == "third" {
			return
		}
	}
	t.Fatalf("sparse tool call was lost: %+v", ds)
}

// Sentinel error to make sure error wrapping works.
var errTestNetwork = errors.New("test network")

func TestErrorWrapping(t *testing.T) {
	// Force an error path and check the wrapping.
	p, _ := NewOpenAI(OpenAIConfig{
		BaseURL: "http://127.0.0.1:1",
		APIKey:  "k",
		Model:   "gpt-4o",
		HTTPClient: &http.Client{
			Timeout: 100 * time.Millisecond,
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("%w: simulated", errTestNetwork)
			}),
		},
	})
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	ds := drainDeltas(t, ch)
	if len(ds) != 1 || ds[0].Err == nil {
		t.Fatalf("expected error delta, got %+v", ds)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenCodeZenAnonymousRequestUsesPublicClientHeaders(t *testing.T) {
	var got http.Header
	p, err := NewOpenAI(OpenAIConfig{
		BaseURL: "https://opencode.ai/zen/v1",
		Model:   "mimo-v2.5-free",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			got = r.Header.Clone()
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n" +
						"data: [DONE]\n\n")),
				Request: r,
			}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for delta := range stream {
		if delta.Err != nil {
			t.Fatal(delta.Err)
		}
	}
	if auth := got.Get("Authorization"); auth != "Bearer public" {
		t.Fatalf("Authorization = %q, want public OpenCode token", auth)
	}
	if client := got.Get("X-OpenCode-Client"); client != "supercli" {
		t.Fatalf("X-OpenCode-Client = %q", client)
	}
	if userAgent := got.Get("User-Agent"); userAgent != "SuperCLI/1.0" {
		t.Fatalf("User-Agent = %q", userAgent)
	}
}

// TestBuildOpenAIRequest_ToolParametersIsObject verifies that
// tool parameters are serialized as a raw JSON object (not a
// JSON-escaped string). LM Studio rejects parameters as string.
func TestBuildOpenAIRequest_ToolParametersIsObject(t *testing.T) {
	tools := []ToolDef{
		{
			Name:        "search",
			Description: "Search the web.",
			Schema:      `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`,
		},
	}
	body, err := buildOpenAIRequest("gpt-4o", []Message{
		{Role: RoleUser, Content: "hi"},
	}, tools, false, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// The parameters must appear as a JSON object, NOT as an
	// escaped string. That means the raw bytes must contain:
	// "parameters":{"type":"object"
	// and NOT: "parameters":"{\"type\":\"object\""
	bodyStr := string(body)
	if strings.Contains(bodyStr, `"parameters":"`) {
		t.Errorf("parameters is a STRING, should be an object:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, `"parameters":{"`) {
		t.Errorf("parameters should be a JSON object:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, `"type":"object"`) {
		t.Errorf("parameters missing type field:\n%s", bodyStr)
	}
}

func TestBuildOpenAIRequest_AdvertisesParallelToolCallsOnlyWithTools(t *testing.T) {
	msg := []Message{{Role: RoleUser, Content: "inspect"}}
	tool := []ToolDef{{
		Name:   "read_lines",
		Schema: `{"type":"object","properties":{"path":{"type":"string"}}}`,
	}}

	withTools, err := buildOpenAIRequest("model", msg, tool, false, false)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(withTools, &got); err != nil {
		t.Fatal(err)
	}
	if parallel, ok := got["parallel_tool_calls"].(bool); !ok || !parallel {
		t.Fatalf("parallel_tool_calls=%#v want true", got["parallel_tool_calls"])
	}

	withoutTools, err := buildOpenAIRequest("model", msg, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	got = nil
	if err := json.Unmarshal(withoutTools, &got); err != nil {
		t.Fatal(err)
	}
	if _, exists := got["parallel_tool_calls"]; exists {
		t.Fatalf("plain chat request unexpectedly contains parallel_tool_calls: %s", withoutTools)
	}
}

func init() { _ = strings.TrimSpace }
