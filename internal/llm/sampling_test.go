package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func f64(v float64) *float64 { return &v }

// TestSampling_EmptyConfigSendsNothing is the load-bearing contract:
// with no sampling configured the request body must not mention a single
// sampling parameter, so the server's own settings still apply. A
// hard-coded default here would silently override every user's server.
func TestSampling_EmptyConfigSendsNothing(t *testing.T) {
	msgs := []Message{{Role: RoleUser, Content: "hi"}}
	body, err := buildOpenAIRequestWithReasoningKey("qwen-local", "qwen-local", msgs, nil, false, false, openAIReasoningNone, 0, Sampling{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, key := range []string{"temperature", "top_p", "top_k", "min_p", "seed", "presence_penalty", "frequency_penalty", "repeat_penalty"} {
		if strings.Contains(string(body), `"`+key+`"`) {
			t.Errorf("empty sampling still emitted %q: %s", key, body)
		}
	}
}

func TestSampling_OpenAIPassThrough(t *testing.T) {
	seed := int64(7)
	topK := 40
	s := Sampling{
		Temperature:      f64(0.6),
		TopP:             f64(0.95),
		TopK:             &topK,
		MinP:             f64(0.05),
		PresencePenalty:  f64(0.1),
		FrequencyPenalty: f64(0.2),
		RepeatPenalty:    f64(1.05),
		Seed:             &seed,
	}
	body, err := buildOpenAIRequestWithReasoningKey("qwen-local", "qwen-local", []Message{{Role: RoleUser, Content: "hi"}}, nil, false, false, openAIReasoningNone, 0, s)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]float64{
		"temperature": 0.6, "top_p": 0.95, "top_k": 40, "min_p": 0.05,
		"presence_penalty": 0.1, "frequency_penalty": 0.2, "repeat_penalty": 1.05, "seed": 7,
	}
	for k, v := range want {
		f, ok := got[k].(float64)
		if !ok || f != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

// TestSampling_ZeroTemperatureIsSent guards the pointer design: 0 is a
// legitimate (greedy) setting and must not be mistaken for "unset".
func TestSampling_ZeroTemperatureIsSent(t *testing.T) {
	body, err := buildOpenAIRequestWithReasoningKey("m", "m", []Message{{Role: RoleUser, Content: "hi"}}, nil, false, false, openAIReasoningNone, 0, Sampling{Temperature: f64(0)})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(string(body), `"temperature":0`) {
		t.Errorf("temperature=0 was dropped: %s", body)
	}
}

// TestSampling_CloudDropsLocalExtensions: top_k/min_p/repeat_penalty are
// llama.cpp extensions; cloud OpenAI answers 400 to unknown fields.
func TestSampling_CloudDropsLocalExtensions(t *testing.T) {
	topK := 40
	s := Sampling{Temperature: f64(0.6), TopK: &topK, MinP: f64(0.05), RepeatPenalty: f64(1.1)}
	SetSamplingDefault(s)
	defer SetSamplingDefault(Sampling{})

	cloud, err := NewOpenAI(OpenAIConfig{BaseURL: "https://api.openai.com/v1", Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("NewOpenAI cloud: %v", err)
	}
	if cloud.sampling.TopK != nil || cloud.sampling.MinP != nil || cloud.sampling.RepeatPenalty != nil {
		t.Errorf("cloud provider kept llama.cpp extensions: %s", cloud.sampling)
	}
	if cloud.sampling.Temperature == nil || *cloud.sampling.Temperature != 0.6 {
		t.Errorf("cloud provider dropped temperature: %s", cloud.sampling)
	}

	local, err := NewOpenAI(OpenAIConfig{BaseURL: "http://127.0.0.1:1234/v1", Model: "qwen"})
	if err != nil {
		t.Fatalf("NewOpenAI local: %v", err)
	}
	if local.sampling.TopK == nil || *local.sampling.TopK != 40 {
		t.Errorf("local provider dropped top_k: %s", local.sampling)
	}
}

// TestSampling_ExplicitConfigBeatsGlobal pins the resolution order used
// by every provider constructor.
func TestSampling_ExplicitConfigBeatsGlobal(t *testing.T) {
	SetSamplingDefault(Sampling{Temperature: f64(0.1)})
	defer SetSamplingDefault(Sampling{})
	p, err := NewOpenAI(OpenAIConfig{BaseURL: "http://127.0.0.1:1234/v1", Model: "qwen", Sampling: Sampling{Temperature: f64(0.9)}})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	if p.sampling.Temperature == nil || *p.sampling.Temperature != 0.9 {
		t.Errorf("explicit sampling lost: %s", p.sampling)
	}
}

func TestSampling_AnthropicOnlySupportedFields(t *testing.T) {
	topK := 40
	seed := int64(3)
	s := Sampling{Temperature: f64(0.4), TopP: f64(0.9), TopK: &topK, FrequencyPenalty: f64(0.5), Seed: &seed}
	body, err := buildAnthropicRequestWithSampling("claude-sonnet-4-5", []Message{{Role: RoleUser, Content: "hi"}}, nil, false, 1024, s.anthropicOnly())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, `"temperature":0.4`) || !strings.Contains(text, `"top_p":0.9`) || !strings.Contains(text, `"top_k":40`) {
		t.Errorf("anthropic dropped supported sampling: %s", text)
	}
	for _, key := range []string{"frequency_penalty", "presence_penalty", "repeat_penalty", "min_p", "seed"} {
		if strings.Contains(text, `"`+key+`"`) {
			t.Errorf("anthropic received unsupported %q: %s", key, text)
		}
	}
}

// TestSampling_ResponsesSkipsReasoningModels: reasoning models reject
// temperature/top_p, so the Responses path must not forward them.
func TestSampling_ResponsesSkipsReasoningModels(t *testing.T) {
	s := Sampling{Temperature: f64(0.4), TopP: f64(0.9)}
	plain, err := prepareStandardResponsesRequest([]byte(`{"model":"plain","include":[]}`), "k", false, s)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !strings.Contains(string(plain), `"temperature":0.4`) {
		t.Errorf("non-reasoning model lost temperature: %s", plain)
	}
	reasoning, err := prepareStandardResponsesRequest([]byte(`{"model":"o3","include":[]}`), "k", true, s)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if strings.Contains(string(reasoning), "temperature") || strings.Contains(string(reasoning), "top_p") {
		t.Errorf("reasoning model received sampling: %s", reasoning)
	}
}

func TestSampling_StringTelemetry(t *testing.T) {
	if got := (Sampling{}).String(); got != "none" {
		t.Errorf("empty String() = %q, want \"none\"", got)
	}
	if got := (Sampling{Temperature: f64(0.6), TopP: f64(0.95)}).String(); got != "temperature=0.6 top_p=0.95" {
		t.Errorf("String() = %q", got)
	}
}
