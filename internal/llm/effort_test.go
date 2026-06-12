package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSetReasoningEffort_Validation(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort("") })
	for _, l := range ReasoningEffortLevels {
		if err := SetReasoningEffort(l); err != nil {
			t.Errorf("SetReasoningEffort(%q): %v", l, err)
		}
		if got := ReasoningEffort(); got != l {
			t.Errorf("ReasoningEffort() = %q, want %q", got, l)
		}
	}
	if err := SetReasoningEffort("turbo"); err == nil {
		t.Error("SetReasoningEffort(turbo): want error")
	}
	if err := SetReasoningEffort(""); err != nil {
		t.Errorf("clear: %v", err)
	}
	if got := ReasoningEffort(); got != "" {
		t.Errorf("after clear: %q", got)
	}
}

func TestSupportsReasoningEffort(t *testing.T) {
	yes := []string{"gpt-5.5", "gpt-5.1-codex-max", "o3-mini", "o4-mini", "openai/gpt-5.5", "codex-mini-latest"}
	no := []string{"gpt-4o", "gpt-4o-mini", "qwen2.5-7b", "llama-3-8b", "claude-sonnet"}
	for _, m := range yes {
		if !SupportsReasoningEffort(m) {
			t.Errorf("SupportsReasoningEffort(%q) = false, want true", m)
		}
	}
	for _, m := range no {
		if SupportsReasoningEffort(m) {
			t.Errorf("SupportsReasoningEffort(%q) = true, want false", m)
		}
	}
}

func TestBuildOpenAIRequest_ReasoningEffort(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort("") })
	msgs := []Message{{Role: RoleUser, Content: "hi"}}

	// Effort set + supporting model → field present.
	if err := SetReasoningEffort("high"); err != nil {
		t.Fatal(err)
	}
	body, err := buildOpenAIRequest("gpt-5.5", msgs, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if req["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v, want high", req["reasoning_effort"])
	}

	// Non-supporting model → field absent.
	body, _ = buildOpenAIRequest("qwen2.5-7b", msgs, nil, false)
	if strings.Contains(string(body), "reasoning_effort") {
		t.Error("reasoning_effort sent to non-supporting model")
	}

	// Effort unset → field absent everywhere.
	_ = SetReasoningEffort("")
	body, _ = buildOpenAIRequest("gpt-5.5", msgs, nil, false)
	if strings.Contains(string(body), "reasoning_effort") {
		t.Error("reasoning_effort sent with no level set")
	}
}

func TestBuildCodexRequest_ReasoningEffort(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort("") })
	msgs := []Message{{Role: RoleUser, Content: "hi"}}

	if err := SetReasoningEffort("xhigh"); err != nil {
		t.Fatal(err)
	}
	body, err := buildCodexRequest("gpt-5.5-codex", msgs, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Reasoning *codexReasoning `json:"reasoning"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if req.Reasoning == nil || req.Reasoning.Effort != "xhigh" {
		t.Errorf("reasoning = %+v, want effort xhigh", req.Reasoning)
	}

	// "none" is never sent to the ChatGPT backend.
	_ = SetReasoningEffort("none")
	body, _ = buildCodexRequest("gpt-5.5-codex", msgs, nil, true)
	if strings.Contains(string(body), `"reasoning"`) {
		t.Error(`effort "none" must not be sent to the codex backend`)
	}
}
