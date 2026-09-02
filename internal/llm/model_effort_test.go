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
	t.Cleanup(clearReasoningEffortSupport)
	yes := []string{"gpt-5.5", "gpt-5.1-codex-max", "o3-mini", "o4-mini", "openai/gpt-5.5", "codex-mini-latest"}
	no := []string{"gpt-4o", "gpt-4o-mini", "qwen2.5-7b", "llama-3-8b"}
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
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
	msgs := []Message{{Role: RoleUser, Content: "hi"}}

	// Effort set + supporting model → field present.
	if err := SetReasoningEffort("high"); err != nil {
		t.Fatal(err)
	}
	body, err := buildOpenAIRequest("gpt-5.5", msgs, nil, false, false)
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
	body, _ = buildOpenAIRequest("qwen2.5-7b", msgs, nil, false, false)
	if strings.Contains(string(body), "reasoning_effort") {
		t.Error("reasoning_effort sent to non-supporting model")
	}

	// Effort unset → field absent everywhere.
	_ = SetReasoningEffort("")
	body, _ = buildOpenAIRequest("gpt-5.5", msgs, nil, false, false)
	if strings.Contains(string(body), "reasoning_effort") {
		t.Error("reasoning_effort sent with no level set")
	}
}

func TestBuildOpenAIRequest_CapabilityUsesUnifiedReasoning(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
	if err := SetReasoningEffort("high"); err != nil {
		t.Fatal(err)
	}
	body, err := buildOpenAIRequestWithReasoning("custom-thinker", []Message{{Role: RoleUser, Content: "hi"}}, nil, false, false, openAIReasoningUnified)
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	reasoning, ok := req["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("reasoning = %#v, want effort high", req["reasoning"])
	}
	if exclude, ok := reasoning["exclude"].(bool); !ok || exclude {
		t.Fatalf("reasoning = %#v, want exclude=false so exposed thinking reaches the UI", req["reasoning"])
	}
	if _, ok := req["reasoning_effort"]; ok {
		t.Fatalf("unified request also contains reasoning_effort: %s", body)
	}
}

func TestBuildCodexRequest_ReasoningEffort(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
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

func TestNextReasoningEffortCycle(t *testing.T) {
	// Without xhigh.
	want := []string{"minimal", "low", "medium", "high", ""}
	cur := ""
	for i, w := range want {
		cur = NextReasoningEffort(cur, false)
		if cur != w {
			t.Fatalf("step %d: got %q, want %q", i, cur, w)
		}
	}
	// With xhigh.
	if got := NextReasoningEffort("high", true); got != "xhigh" {
		t.Errorf("high+xhigh -> %q, want xhigh", got)
	}
	if got := NextReasoningEffort("xhigh", true); got != "" {
		t.Errorf("xhigh -> %q, want off", got)
	}
	// Out-of-cycle level restarts at off.
	if got := NextReasoningEffort("none", false); got != "" {
		t.Errorf("none -> %q, want off", got)
	}
}

func TestSupportsXHighReasoningEffort(t *testing.T) {
	t.Cleanup(clearReasoningEffortSupport)
	yes := []string{
		"openai/gpt-5.1-codex-max",
		"gpt-5.5",
		"openai/gpt-5.5",
		"gpt-5.5-codex",
		"gpt-5.3-codex",
		"codex-mini-latest",
	}
	no := []string{"gpt-5", "gpt-5.1", "gpt-4o", "o3-mini", "qwen2.5-7b"}
	for _, m := range yes {
		if !SupportsXHighReasoningEffort(m) {
			t.Errorf("SupportsXHighReasoningEffort(%q) = false, want true", m)
		}
	}
	for _, m := range no {
		if SupportsXHighReasoningEffort(m) {
			t.Errorf("SupportsXHighReasoningEffort(%q) = true, want false", m)
		}
	}
}

func TestReasoningEffortErrorHint(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })

	// No effort set → no hint.
	_ = SetReasoningEffort("")
	if got := ReasoningEffortErrorHint(`{"error":"unsupported reasoning effort"}`); got != "" {
		t.Errorf("hint with no effort set: %q", got)
	}

	_ = SetReasoningEffort("xhigh")
	if got := ReasoningEffortErrorHint(`{"error":{"message":"Invalid value for reasoning_effort"}}`); !strings.Contains(got, "xhigh") || !strings.Contains(got, "/reasoning") {
		t.Errorf("hint = %q, want mention of xhigh and /reasoning", got)
	}
	// Unrelated 400 body → no hint.
	if got := ReasoningEffortErrorHint(`{"error":"context length exceeded"}`); got != "" {
		t.Errorf("hint for unrelated error: %q", got)
	}
	// Only 400s get the hint.
	if got := badRequestEffortHint(500, []byte("reasoning effort oops")); got != "" {
		t.Errorf("hint for 500: %q", got)
	}
	if got := badRequestEffortHint(400, []byte("reasoning effort oops")); got == "" {
		t.Error("400 effort error: want hint")
	}
}

func TestParseReasoningEffortError_SupportedValues(t *testing.T) {
	body := `{
		"error": {
			"message": "Unsupported value: 'minimal' is not supported with the 'gpt-5.5' model. Supported values are: 'none', 'low', 'medium', 'high', and 'xhigh'.",
			"type": "invalid_request_error",
			"param": "reasoning_effort",
			"code": "unsupported_value"
		}
	}`
	info, ok := ParseReasoningEffortError(body)
	if !ok {
		t.Fatal("ParseReasoningEffortError: ok=false")
	}
	if info.Requested != "minimal" {
		t.Fatalf("requested=%q want minimal", info.Requested)
	}
	want := []string{"none", "low", "medium", "high", "xhigh"}
	if strings.Join(info.Supported, "|") != strings.Join(want, "|") {
		t.Fatalf("supported=%v want %v", info.Supported, want)
	}
}

func TestLearnReasoningEffort_MinimalFallsForwardToLow(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
	if err := SetReasoningEffort("minimal"); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"error":{"message":"Unsupported value: 'minimal' is not supported. Supported values are: 'none', 'low', 'medium', 'high', and 'xhigh'.","param":"reasoning_effort"}}`)
	effort, ok := LearnReasoningEffortFromError("openai/gpt-5.5", 400, body)
	if !ok || effort != "low" {
		t.Fatalf("learn retry effort=(%q,%v), want (low,true)", effort, ok)
	}
	if got := ReasoningEffortForModel("openai/gpt-5.5"); got != "low" {
		t.Fatalf("ReasoningEffortForModel = %q, want low", got)
	}
	if supported, ok := SupportedReasoningEfforts("openai/gpt-5.5"); !ok || strings.Join(supported, "|") != "none|low|medium|high|xhigh" {
		t.Fatalf("supported=(%v,%v), want learned values", supported, ok)
	}
}

func TestLearnReasoningEffort_UnsupportedParameterIsOmitted(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
	if err := SetReasoningEffort("high"); err != nil {
		t.Fatal(err)
	}
	effort, retry := LearnReasoningEffortFromError("custom-thinker", 400, []byte(`{"error":{"message":"reasoning parameter is not supported"}}`))
	if !retry || effort != "" {
		t.Fatalf("LearnReasoningEffortFromError = (%q, %v), want empty retry", effort, retry)
	}
	if SupportsReasoningEffortWithCapability("custom-thinker", true) {
		t.Fatal("live rejection must override the capability fallback")
	}
	if got := ReasoningEffortForModelWithCapability("custom-thinker", true); got != "" {
		t.Fatalf("effective effort = %q, want omitted", got)
	}
}

func TestLearnReasoningEffort_InvalidQuotedValueIsNotLearnedAsSupported(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
	if err := SetReasoningEffort("high"); err != nil {
		t.Fatal(err)
	}
	effort, retry := LearnReasoningEffortFromError("custom-thinker", 400, []byte(`{"error":{"message":"Invalid value 'high' for reasoning_effort"}}`))
	if !retry || effort != "" {
		t.Fatalf("LearnReasoningEffortFromError = (%q, %v), want empty retry", effort, retry)
	}
	if supported, ok := SupportedReasoningEfforts("custom-thinker"); !ok || len(supported) != 0 {
		t.Fatalf("supported = (%v, %v), want learned unsupported sentinel", supported, ok)
	}
}

func TestReasoningSupportEvidenceIsScoped(t *testing.T) {
	t.Cleanup(clearReasoningEffortSupport)
	a := ReasoningSupportKey("https://a.example/v1", "vendor/model")
	b := ReasoningSupportKey("https://b.example/v1", "vendor/model")
	setReasoningEffortUnsupported(a)
	if SupportsReasoningEffortWithCapability(a, true) {
		t.Fatal("rejected endpoint still supports reasoning")
	}
	if !SupportsReasoningEffortWithCapability(b, true) {
		t.Fatal("evidence from endpoint A leaked into endpoint B")
	}
}

func TestOpenAIReasoningFormat_UsesGatewayAndCapabilities(t *testing.T) {
	t.Cleanup(clearReasoningEffortSupport)
	caps := NewCapabilityRegistry()
	caps.Register(ModelInfo{ID: "vendor/custom-thinker", Reasoning: true, Source: SourceProvider})
	p, err := NewOpenAI(OpenAIConfig{
		BaseURL:      "https://openrouter.ai/api/v1",
		Model:        "vendor/custom-thinker",
		Capabilities: caps,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.reasoningFormat(); got != openAIReasoningUnified {
		t.Fatalf("reasoningFormat = %v, want unified", got)
	}
	SetReasoningEffortSupport(p.reasoningKey(), []string{"low", "medium", "high"})
	if got := p.reasoningFormat(); got != openAIReasoningUnified {
		t.Fatalf("reasoningFormat after learning = %v, want stable unified", got)
	}
}

func TestUnifiedReasoningGatewayDoesNotRequireModelMetadata(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
	if err := SetReasoningEffort("medium"); err != nil {
		t.Fatal(err)
	}
	for _, baseURL := range []string{
		"https://openrouter.ai/api/v1",
		"https://api.kilo.ai/api/openrouter",
		"https://opencode.ai/zen/v1",
	} {
		p, err := NewOpenAI(OpenAIConfig{BaseURL: baseURL, Model: "model-without-capability-metadata"})
		if err != nil {
			t.Fatal(err)
		}
		if got := p.reasoningFormat(); got != openAIReasoningUnified {
			t.Errorf("%s: reasoningFormat = %v, want unified", baseURL, got)
		}
	}
}

func TestUnknownOpenAICompatibleModelTriesUnifiedReasoning(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
	if err := SetReasoningEffort("low"); err != nil {
		t.Fatal(err)
	}
	p, err := NewOpenAI(OpenAIConfig{
		BaseURL: "https://custom.example/v1",
		Model:   "plain-model-without-metadata",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.reasoningFormat(); got != openAIReasoningUnified {
		t.Fatalf("reasoningFormat = %v, want best-effort unified", got)
	}
}

func TestOpencodeReasoningCapabilityUsesNamespacedRegistryID(t *testing.T) {
	t.Cleanup(clearReasoningEffortSupport)
	caps := NewCapabilityRegistry()
	caps.Register(ModelInfo{ID: "opencode/vendor/custom-thinker", Reasoning: true, Source: SourceProvider})
	p, err := NewOpencode(OpencodeConfig{
		BaseURL:      "http://localhost:4096/v1",
		Model:        "vendor/custom-thinker",
		Capabilities: caps,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !p.inner.hasReasoningCapability() {
		t.Fatal("inner provider lost namespaced Opencode reasoning capability")
	}
	if got := p.inner.reasoningFormat(); got != openAIReasoningUnified {
		t.Fatalf("reasoningFormat = %v, want unified", got)
	}
}

func TestBuildCodexRequest_UsesLearnedEffort(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
	_ = SetReasoningEffort("minimal")
	SetReasoningEffortSupport("gpt-5.5", []string{"none", "low", "medium", "high", "xhigh"})
	body, err := buildCodexRequest("gpt-5.5", []Message{{Role: RoleUser, Content: "hi"}}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Reasoning *codexReasoning `json:"reasoning"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if req.Reasoning == nil || req.Reasoning.Effort != "low" {
		t.Fatalf("reasoning=%+v, want low", req.Reasoning)
	}
}

func TestPatchCodexReasoningEffort(t *testing.T) {
	in := []byte(`{"model":"gpt-5.5","reasoning":{"effort":"minimal","summary":"auto"},"stream":true}`)
	out, ok := patchCodexReasoningEffort(in, "low")
	if !ok {
		t.Fatal("patchCodexReasoningEffort low: ok=false")
	}
	var req struct {
		Reasoning *codexReasoning `json:"reasoning"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	if req.Reasoning == nil || req.Reasoning.Effort != "low" || req.Reasoning.Summary != "auto" {
		t.Fatalf("reasoning=%+v, want low/auto", req.Reasoning)
	}
	out, ok = patchCodexReasoningEffort(out, "none")
	if !ok {
		t.Fatal("patchCodexReasoningEffort none: ok=false")
	}
	if strings.Contains(string(out), `"reasoning"`) {
		t.Fatalf("reasoning should be removed for none: %s", out)
	}
}
