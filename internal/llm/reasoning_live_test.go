package llm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Explicit opt-in: a bounded, tool-free inference test against the user's local
// model. Ordinary go test runs never contact LM Studio.
func TestReasoningLMStudioLive(t *testing.T) {
	base := os.Getenv("SUPERCLI_REASONING_EVAL_URL")
	if base == "" {
		t.Skip("set SUPERCLI_REASONING_EVAL_URL to run the local reasoning check")
	}
	model := os.Getenv("SUPERCLI_REASONING_EVAL_MODEL")
	report := os.Getenv("SUPERCLI_REASONING_EVAL_REPORT")
	if !IsLocalBaseURL(base) || model == "" || report == "" {
		t.Fatal("local URL, model and report path required")
	}
	t.Cleanup(func() { _ = SetReasoningEffort("") })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	caps := NewCapabilityRegistry()
	discovered := ListLocalNativeModelInfos(ctx, base, "")
	caps.RegisterAll(discovered)
	info, found := caps.Get(model)
	if !found || !info.ReasoningKnown {
		t.Fatal("local model reasoning metadata was not parsed")
	}
	p, err := NewOpenAI(OpenAIConfig{BaseURL: base, Model: model, MaxTokens: 32, Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		Level          string         `json:"level"`
		State          ReasoningState `json:"state"`
		ReasoningChars int            `json:"reasoning_chars"`
		Answer         string         `json:"answer"`
		Usage          *Usage         `json:"usage,omitempty"`
		ElapsedMS      int64          `json:"elapsed_ms"`
	}
	var results []result
	for _, level := range []string{"low", "none", "high"} {
		_ = SetReasoningEffort(level)
		got := result{Level: level, State: ProviderReasoningState(p)}
		start := time.Now()
		ch, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "Reply with just OK."}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for d := range ch {
			if d.Err != nil {
				t.Fatal(d.Err)
			}
			got.ReasoningChars += len(d.Reasoning)
			got.Answer += d.Content
			if d.Usage != nil {
				got.Usage = d.Usage
			}
		}
		got.ElapsedMS = time.Since(start).Milliseconds()
		results = append(results, got)
		if level == "none" && got.ReasoningChars != 0 {
			t.Errorf("none still returned %d reasoning bytes", got.ReasoningChars)
		}
		if got.Answer == "" {
			t.Errorf("%s returned no answer", level)
		}
		t.Logf("level=%s effective=%s reasoning_bytes=%d elapsed_ms=%d", level, got.State.Effective, got.ReasoningChars, got.ElapsedMS)
	}
	if err := os.MkdirAll(filepath.Dir(report), 0755); err != nil {
		t.Fatal(err)
	}
	body, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(report, body, 0600); err != nil {
		t.Fatal(err)
	}
}
