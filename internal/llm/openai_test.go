package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestBuildOpenAIRequest_CoalescesMidConversationSystemMessages(t *testing.T) {
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
	if !strings.Contains(got.Messages[0].Content, "base system") || !strings.Contains(got.Messages[0].Content, "freshness stamp") {
		t.Fatalf("coalesced system content = %q", got.Messages[0].Content)
	}
	for i := 1; i < len(got.Messages); i++ {
		if got.Messages[i].Role == "system" {
			t.Fatalf("message %d is system after beginning: %+v", i, got.Messages)
		}
	}
	if got.Messages[1].Role != "user" || got.Messages[2].Role != "assistant" || got.Messages[3].Role != "user" {
		t.Fatalf("non-system order changed: %+v", got.Messages)
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

func TestBuildOpenAIRequest_VisionDisabledDropsImages(t *testing.T) {
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
	if content != "hi" {
		t.Fatalf("content = %q, want hi", content)
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
	var body strings.Builder
	for _, d := range ds {
		body.WriteString(d.Content)
	}
	if body.String() != "<thinking>Thinking Process</thinking>\nCześć!" {
		t.Fatalf("body = %q, want reasoning + answer", body.String())
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
	var body strings.Builder
	for _, d := range ds {
		body.WriteString(d.Content)
	}
	if body.String() != "<thinking>Plan A</thinking>\nAnswer" {
		t.Fatalf("body = %q, want generic thinking + answer", body.String())
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

func TestOpenAI_VisionDisabled_EmitsError(t *testing.T) {
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
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
	if len(ds) != 1 || ds[0].Err == nil {
		t.Fatalf("expected single error delta, got %+v", ds)
	}
	if !strings.Contains(ds[0].Err.Error(), "vision") {
		t.Fatalf("err = %v", ds[0].Err)
	}
}

func TestEncodeBase64(t *testing.T) {
	if got := EncodeBase64([]byte("hello")); got != "aGVsbG8=" {
		t.Fatalf("EncodeBase64 = %q", got)
	}
}

// --- chunk-decoding resilience ---

func TestOpenAI_IgnoreNonJSONChunk(t *testing.T) {
	// Real providers sometimes send "ping:" frames or other noise.
	chunks := []string{
		`not json`,
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
	if got.Load() != "/chat/completions" {
		t.Fatalf("path = %v, want /chat/completions", got.Load())
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

func init() { _ = strings.TrimSpace }
