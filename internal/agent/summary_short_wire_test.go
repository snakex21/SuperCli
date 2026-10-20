package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestAutoSummarizerUsesEvaluatedStatePrompt(t *testing.T) {
	if compactionStatePrompt != shortSummaryExperimentPrompt || compactionStateTemplate != shortSummaryExperimentTemplate {
		t.Fatal("production instruction differs from the evaluated candidate")
	}
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "No new dependencies; keep my reasoning and all earlier requirements."}}
	msgs = append(msgs, processReadinessFixturePair("checked", `{"id":"proc-1","status":"stopped","ready":false,"ready_at":"2026-10-19T10:30:00Z"}`)...)
	provider := func() *stubProvider {
		return &stubProvider{name: "summary", scripts: [][]llm.Delta{{{Content: "Goal: continue\nDone: verified earlier\nState: stopped\nPending: review"}, {FinishReason: "stop"}}}}
	}
	p := provider()
	out, err := NewAutoSummarizer(nil)(context.Background(), p, msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.reqs) != 1 || p.reqs[0][0].Content != compactionStatePrompt || !reflect.DeepEqual(decodeCompactUserContext(t, out), decodeCompactUserContext(t, compactPreservedUserContext(msgs))) || !strings.Contains(out, "past_ready_observed_at=2026-10-19T10:30:00Z") {
		t.Fatal("auto summary changed evaluated prompt, exact carry or process facts")
	}
	p = provider()
	if _, err = SummarizeForCompaction(context.Background(), p, msgs); err != nil || len(p.reqs) != 1 || p.reqs[0][0].Content != compactionPrompt {
		t.Fatal("standalone summary lost its self-contained contract")
	}
}

// Actual request builders for the common transport families: changing only
// the summary instruction must preserve reasoning, sampling and output budgets.
func TestShortSummaryPromptDoesNotChangeProviderSettings(t *testing.T) {
	for _, protocol := range []string{"chat", "anthropic", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			requests := make(chan map[string]any, 2)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "invalid JSON", 400)
					return
				}
				requests <- body
				w.Header().Set("Content-Type", "text/event-stream")
				text := `"Goal: continue\nDone: checked\nState: portable data; no publishing\nPending: review"`
				switch protocol {
				case "chat":
					fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", text)
				case "responses":
					fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":%s}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":20}}}\n\n", text)
				case "anthropic":
					fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"role\":\"assistant\",\"usage\":{\"input_tokens\":100}}}\n\n")
					fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
					fmt.Fprintf(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%s}}\n\n", text)
					fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":20}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				}
			}))
			defer srv.Close()
			var p llm.Provider
			var err error
			switch protocol {
			case "chat":
				p, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "gpt-6.1-sol", MaxTokens: 4321})
			case "anthropic":
				p, err = llm.NewAnthropic(llm.AnthropicConfig{BaseURL: srv.URL + "/v1", Model: "claude-opus-5-5", MaxTokens: 12345})
			case "responses":
				p, err = llm.NewResponses(llm.ResponsesConfig{BaseURL: srv.URL + "/v1", Model: "gpt-6.1-sol"})
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx := llm.WithReasoningEffort(context.Background(), "max")
			messages := []llm.Message{{Role: llm.RoleUser, Content: "Fix parser; no publishing; keep portable data."}}
			for _, prompt := range []string{compactionPrompt, shortSummaryExperimentPrompt} {
				out, err := NewAutoSummarizer(nil)(ctx, shortSummaryPromptProvider{p, prompt}, messages)
				if err != nil {
					t.Fatal(err)
				}
				carry := decodeCompactUserContext(t, out)
				if len(carry.Requests) != 1 || carry.Requests[0] != messages[0].Content {
					t.Fatal("request not retained exactly")
				}
			}
			first, second := <-requests, <-requests
			var normalize func(any) any
			normalize = func(v any) any {
				switch value := v.(type) {
				case string:
					if value == compactionPrompt || value == shortSummaryExperimentPrompt {
						return "<summary instruction>"
					}
					return value
				case map[string]any:
					for k, x := range value {
						value[k] = normalize(x)
					}
				case []any:
					for i, x := range value {
						value[i] = normalize(x)
					}
				}
				return v
			}
			if !reflect.DeepEqual(normalize(first), normalize(second)) {
				t.Fatal("summary format changed other provider request fields")
			}
			if llm.ReasoningEffort(ctx) != "max" {
				t.Fatal("reasoning context changed")
			}
			raw, _ := json.Marshal(first)
			if strings.Contains(string(raw), "max_tokens\":200") || strings.Contains(string(raw), "max_output_tokens\":200") {
				t.Fatal("short output preference became a truncation budget")
			}
		})
	}
}
