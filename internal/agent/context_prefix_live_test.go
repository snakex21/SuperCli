package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Opt-in, synthetic text only. Never executes tools, edits a repository,
// changes model-server settings or changes the application's reasoning setting.
func TestContextPrefixLMStudioLive(t *testing.T) {
	base := os.Getenv("SUPERCLI_PREFIX_EVAL_URL")
	if base == "" {
		t.Skip("set SUPERCLI_PREFIX_EVAL_URL for a bounded local cache test")
	}
	model, report := os.Getenv("SUPERCLI_PREFIX_EVAL_MODEL"), os.Getenv("SUPERCLI_PREFIX_EVAL_REPORT")
	if !llm.IsLocalBaseURL(base) || model == "" || report == "" {
		t.Fatal("local endpoint, model and report required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	caps := llm.NewCapabilityRegistry()
	caps.RegisterAll(llm.ListLocalNativeModelInfos(ctx, base, ""))
	provider, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: base, Model: model, MaxTokens: 24, Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	if err := llm.SetReasoningEffort("none"); err != nil {
		t.Fatal(err)
	}
	defer llm.SetReasoningEffort("")
	const system = "Synthetic cache test. Use the latest CURRENT_TAG from context. Reply with exactly that tag and nothing else."
	var body strings.Builder
	for i := 0; i < 450; i++ {
		fmt.Fprintf(&body, "Fixture item %04d: parse input, keep Unicode intact, preserve the recorded result.\n", i)
	}
	history := []llm.Message{
		{Role: llm.RoleUser, Content: body.String()},
		{Role: llm.RoleAssistant, Content: "The fixture is recorded."},
		{Role: llm.RoleUser, Content: "What is CURRENT_TAG?"},
	}
	type result struct {
		Arm         string     `json:"arm"`
		Tag         string     `json:"tag"`
		Answer      string     `json:"answer"`
		TTFTMS      int64      `json:"ttft_ms"`
		ElapsedMS   int64      `json:"elapsed_ms"`
		Usage       *llm.Usage `json:"usage,omitempty"`
		PromptBytes int        `json:"prompt_bytes"`
		Error       string     `json:"error,omitempty"`
	}
	var results []result
	defer func() {
		raw, _ := json.MarshalIndent(results, "", "  ")
		if err := os.WriteFile(report, raw, 0600); err != nil {
			t.Error(err)
		}
	}()
	for _, arm := range []string{"memory-in-prefix", "memory-in-tail"} {
		for _, tag := range []string{"ALPHA", "BRAVO"} {
			note := "[memory_briefing]\nCURRENT_TAG: " + tag + "\n[/memory_briefing]"
			cfg := LoopConfig{Provider: provider, Registry: tools.NewRegistry(), System: system, InitialMessages: history}
			if arm == "memory-in-prefix" {
				cfg.System += "\n\n" + note
			} else {
				cfg.LiveContext = note
			}
			loop, err := NewLoop(cfg)
			if err != nil {
				t.Fatal(err)
			}
			msgs := loop.providerMessages()
			entry := result{Arm: arm, Tag: tag}
			for _, m := range msgs {
				entry.PromptBytes += len(m.TextOnly().Content)
			}
			start := time.Now()
			ch, err := provider.Complete(ctx, msgs, nil)
			if err != nil {
				entry.Error = err.Error()
				results = append(results, entry)
				t.Fatal(err)
			}
			for d := range ch {
				if entry.TTFTMS == 0 && (d.Content != "" || d.Reasoning != "") {
					entry.TTFTMS = time.Since(start).Milliseconds()
				}
				entry.Answer += d.Content
				if d.Usage != nil {
					entry.Usage = d.Usage
				}
				if d.Err != nil {
					entry.Error = d.Err.Error()
				}
			}
			entry.ElapsedMS = time.Since(start).Milliseconds()
			results = append(results, entry)
			t.Logf("%s tag=%s ttft=%dms total=%dms answer=%q", arm, tag, entry.TTFTMS, entry.ElapsedMS, entry.Answer)
			if entry.Error != "" {
				t.Fatal(entry.Error)
			}
			if strings.TrimSpace(entry.Answer) != tag {
				t.Fatalf("memory answer=%q, want %q", entry.Answer, tag)
			}
		}
	}
}
