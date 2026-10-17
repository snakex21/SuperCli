package llm

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRequestReasoningEffortFallback(t *testing.T) {
	previous := ReasoningEffort()
	t.Cleanup(func() { _ = SetReasoningEffort(previous); clearReasoningEffortSupport() })
	_ = SetReasoningEffort("low")
	parent := context.Background()
	worker := WithReasoningEffort(parent, "max")
	model := "gpt-5-worker-context-test"
	body := []byte(`{"error":{"message":"Unsupported value: 'max' is not supported for reasoning_effort with this model. Supported values are: 'low', 'medium', 'high'."}}`)
	if got, retry := LearnReasoningEffortFromError(model, 400, body, worker); !retry || got != "high" {
		t.Fatalf("worker retry = %q, %v", got, retry)
	}
	if got := ReasoningEffortForModel(model, worker); got != "high" {
		t.Fatalf("learned worker effort = %q", got)
	}
	if got := ReasoningEffortForModel(model, parent); got != "low" {
		t.Fatalf("coordinator effort = %q", got)
	}
	if got := ReasoningEffort(WithReasoningEffort(parent, "")); got != "low" {
		t.Fatalf("inherit = %q", got)
	}
	if got := ReasoningEffort(WithReasoningEffort(parent, "invalid")); got != "low" {
		t.Fatalf("invalid override = %q", got)
	}
	// An override works even when the global setting is provider-default.
	_ = SetReasoningEffort("")
	if got, retry := LearnReasoningEffortFromError(model, 400, body, worker); !retry || got != "high" {
		t.Fatalf("override without global setting = %q, %v", got, retry)
	}
}

func TestRequestReasoningEffortProviderDialects(t *testing.T) {
	previous := ReasoningEffort()
	t.Cleanup(func() { _ = SetReasoningEffort(previous) })
	_ = SetReasoningEffort("low")
	worker := WithReasoningEffort(context.Background(), "high")
	body, err := buildAnthropicRequestWithCaching("claude-sonnet-4", nil, nil, false, 32000, Sampling{}, false, worker)
	if err != nil {
		t.Fatal(err)
	}
	var a anthropicRequest
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatal(err)
	}
	expected := anthropicThinkingForModel("claude-sonnet-4", 32000, worker)
	if a.Thinking == nil || expected == nil || *a.Thinking != *expected {
		t.Fatalf("anthropic worker thinking = %+v, want %+v", a.Thinking, expected)
	}
	p := &CodexProvider{cfg: CodexConfig{Model: "gpt-5"}}
	if got := p.reasoningEffort(worker); got != "high" {
		t.Fatalf("codex worker = %q", got)
	}
	if got := p.reasoningEffort(); got != "low" {
		t.Fatalf("codex coordinator = %q", got)
	}
	p.cfg.StandardResponsesAPI = true
	p.cfg.BackendURL = "https://effort-test.invalid/v1"
	if got := p.reasoningEffort(worker); got != "high" {
		t.Fatalf("responses worker = %q", got)
	}
	body, err = prepareOpenCodeZenResponsesRequest([]byte(`{"model":"gpt-5","input":[]}`), "ses_test", worker)
	if err != nil {
		t.Fatal(err)
	}
	var zen struct {
		Reasoning struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(body, &zen); err != nil {
		t.Fatal(err)
	}
	if zen.Reasoning.Effort != "high" {
		t.Fatalf("zen worker = %q", zen.Reasoning.Effort)
	}
}
