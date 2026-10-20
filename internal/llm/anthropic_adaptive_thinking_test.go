package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAnthropicThinkingFormByModel(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
	if err := SetReasoningEffort("xhigh"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		model    string
		adaptive bool
	}{
		{"claude-opus-5-5", true},
		{"claude-sonnet-4-6", true},
		{"claude-opus-4-7", true},
		{"claude-sonnet-4-5", false},
		{"claude-opus-4-1", false},
		{"claude-sonnet-4-20250514", false},
		{"claude-3-7-sonnet-latest", false},
	} {
		body, err := buildAnthropicRequest(tc.model, []Message{{Role: RoleUser, Content: "hi"}}, nil, false, 8192)
		if err != nil {
			t.Fatal(err)
		}
		var req anthropicRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		if req.Thinking == nil {
			t.Fatalf("%s: thinking missing", tc.model)
		}
		if tc.adaptive {
			if req.Thinking.Type != "adaptive" || req.Thinking.BudgetTokens != 0 || req.OutputConfig == nil || req.OutputConfig.Effort != "xhigh" ||
				req.MaxTokens < anthropicAdaptiveMinOutput {
				t.Fatalf("%s: want adaptive+effort, got %s", tc.model, body)
			}
		} else if req.Thinking.Type != "enabled" || req.Thinking.BudgetTokens <= 0 || req.OutputConfig != nil {
			t.Fatalf("%s: want enabled budget, got %s", tc.model, body)
		}
	}
}

// A backend that rejects thinking.type.enabled must switch to adaptive and
// keep the effort, not mark reasoning unsupported ("nie jest wysyłane").
func TestAnthropicEnabledRejectionSwitchesToAdaptive(t *testing.T) {
	const model = "claude-custom-old-name-4-5"
	t.Cleanup(func() {
		_ = SetReasoningEffort("")
		clearReasoningEffortSupport()
		anthropicThinkingMode.Delete(normalizeReasoningModel(model))
	})
	if err := SetReasoningEffort("high"); err != nil {
		t.Fatal(err)
	}
	var bodies []string
	client := &http.Client{Transport: anthropicOwnedBodyTransport(func(req *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(req.Body)
		bodies = append(bodies, string(b))
		if len(bodies) == 1 {
			return anthropicOwnedBodyResponse(req, http.StatusBadRequest,
				`{"type":"error","error":{"type":"invalid_request_error","message":"\"thinking.type.enabled\" is not supported for this model. Use \"thinking.type.adaptive\" and \"output_config.effort\" to control thinking behavior."}}`), nil
		}
		return anthropicOwnedBodyResponse(req, http.StatusOK, "data: {\"type\":\"message_stop\"}\n\n"), nil
	})}
	p, err := NewAnthropic(AnthropicConfig{BaseURL: "https://api.anthropic.com/v1", Model: model, HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for d := range ch {
		if d.Err != nil {
			t.Fatal(d.Err)
		}
	}
	if len(bodies) != 2 || !strings.Contains(bodies[1], `"type":"adaptive"`) || !strings.Contains(bodies[1], `"effort":"high"`) || strings.Contains(bodies[1], "budget_tokens") ||
		!strings.Contains(bodies[1], `"max_tokens":32000`) {
		t.Fatalf("retry body = %v", bodies)
	}
	if levels, learned := SupportedReasoningEfforts(model); learned && len(levels) == 0 {
		t.Fatal("thinking-type rejection marked reasoning unsupported")
	}
	if !anthropicUsesAdaptiveThinking(model) {
		t.Fatal("adaptive form not remembered for the next request")
	}
}
