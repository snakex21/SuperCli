package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const chatToolsEffortRejection = `{"error":{"message":"Function tools with reasoning_effort are not supported for gpt-5.4 in /v1/chat/completions. Please use /v1/responses instead.","type":"invalid_request_error","param":"reasoning_effort","code":null}}`

// Regression: gpt-5.4 rejects reasoning_effort together with function tools
// on /chat/completions. That 400 was learned as "model has no reasoning", so
// the control was greyed out and every later turn silently lost the user's
// level. Only tool turns may drop the effort; tool-free turns keep it.
func TestOpenAIToolsEffortRejectionKeepsReasoningForToolFreeTurns(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
	_ = SetReasoningEffort("high")
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		mu.Lock()
		bodies = append(bodies, m)
		mu.Unlock()
		if _, ok := m["reasoning_effort"]; ok && m["tools"] != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(chatToolsEffortRejection))
			return
		}
		sseResponse(w, `{"choices":[{"index":0,"delta":{"content":"ok"}}]}`, `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	p, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "gpt-5.4"})
	if err != nil {
		t.Fatal(err)
	}
	tools := []ToolDef{{Name: "read", Description: "r", Schema: `{"type":"object","properties":{}}`}}
	run := func(tools []ToolDef) (errs []string, notices []string) {
		ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, tools)
		if err != nil {
			t.Fatal(err)
		}
		for d := range ch {
			if d.Err != nil {
				errs = append(errs, d.Err.Error())
			}
			if d.Notice != "" {
				notices = append(notices, d.Notice)
			}
		}
		return errs, notices
	}
	errs, notices := run(tools)
	if len(errs) > 0 {
		t.Fatalf("tool turn failed: %v", errs)
	}
	if len(bodies) != 2 || bodies[1]["reasoning_effort"] != nil || bodies[1]["tools"] == nil {
		t.Fatalf("want one retry with tools and without effort, got %v", bodies)
	}
	if len(notices) == 0 || !strings.Contains(notices[0], "responses") {
		t.Fatalf("user was not told why reasoning was dropped: %v", notices)
	}
	if levels, learned := SupportedReasoningEfforts(p.reasoningKey()); learned && len(levels) == 0 {
		t.Fatal("combination rejection was learned as 'model has no reasoning'")
	}
	if !ProviderReasoningState(p).Supported {
		t.Fatal("reasoning control was disabled for the model")
	}
	// The next tool turn skips the doomed attempt.
	if errs, _ := run(tools); len(errs) > 0 || len(bodies) != 3 || bodies[2]["reasoning_effort"] != nil {
		t.Fatalf("second tool turn: errs=%v bodies=%d last=%v", errs, len(bodies), bodies[len(bodies)-1])
	}
	// A tool-free turn still carries the user's level.
	if errs, _ := run(nil); len(errs) > 0 || bodies[len(bodies)-1]["reasoning_effort"] != "high" {
		t.Fatalf("tool-free turn lost reasoning: errs=%v body=%v", errs, bodies[len(bodies)-1])
	}
}

// Regression: Chat Completions sent temperature/top_p and max_tokens next to
// reasoning_effort, which OpenAI reasoning models reject with a 400 that no
// retry path recognizes.
func TestOpenAIReasoningFamiliesUseCompletionTokenCapAndDropSampling(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
	temp, topP := 0.2, 0.9
	sampling := Sampling{Temperature: &temp, TopP: &topP}
	for _, tc := range []struct {
		model, effort     string
		format            openAIReasoningFormat
		wantSampling      bool
		wantCompletionCap bool
	}{
		{"gpt-5.4", "high", openAIReasoningEffort, false, true},
		{"gpt-5.4", "none", openAIReasoningEffort, true, true},
		{"o3-mini", "", openAIReasoningEffort, false, true},
		{"gpt-4o", "", openAIReasoningNone, true, false},
		{"qwen-local", "high", openAIReasoningEffort, true, false},
		{"claude-sonnet-4-5", "high", openAIReasoningEffort, true, false},
	} {
		_ = SetReasoningEffort(tc.effort)
		body, err := buildOpenAIRequestWithReasoningKey(tc.model, tc.model, []Message{{Role: RoleUser, Content: "hi"}}, nil, false, false, tc.format, 1000, sampling)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		_, hasTemp := m["temperature"]
		_, hasTopP := m["top_p"]
		if hasTemp != tc.wantSampling || hasTopP != tc.wantSampling {
			t.Errorf("%s/%q: sampling present=%v/%v, want %v; body=%s", tc.model, tc.effort, hasTemp, hasTopP, tc.wantSampling, body)
		}
		_, hasMax := m["max_tokens"]
		_, hasCompletion := m["max_completion_tokens"]
		if hasCompletion != tc.wantCompletionCap || hasMax == tc.wantCompletionCap {
			t.Errorf("%s: max_tokens=%v max_completion_tokens=%v, want completion cap %v; body=%s", tc.model, hasMax, hasCompletion, tc.wantCompletionCap, body)
		}
	}
}
