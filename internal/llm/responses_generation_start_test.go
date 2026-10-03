package llm

import (
	"context"
	"strings"
	"testing"
)

func TestResponsesHiddenReasoningStartPrecedesVisibleOutput(t *testing.T) {
	fixture := strings.Join([]string{
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\",\"id\":\"rs_fixture\"}}",
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}",
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"id\":\"rs_fixture\",\"encrypted_content\":\"opaque-fixture\",\"summary\":[]}}",
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":163,\"total_tokens\":173,\"output_tokens_details\":{\"reasoning_tokens\":151}}}}",
	}, "\n\n") + "\n\n"
	for _, base := range []string{"https://api.openai.com/v1", "https://anyrouter.top/v1", "https://opencode.ai/zen/v1"} {
		t.Run(base, func(t *testing.T) {
			p := &CodexProvider{cfg: CodexConfig{BackendURL: base, Model: "fixture"}}
			ch := make(chan Delta, 16)
			p.streamCodexSSE(context.Background(), strings.NewReader(fixture), ch)
			close(ch)
			started, visible := false, false
			var usage *Usage
			var text string
			for d := range ch {
				if d.Err != nil {
					t.Fatal(d.Err)
				}
				if d.Reasoning != "" {
					t.Fatal("hidden thought was reconstructed")
				}
				if d.ReasoningStarted {
					if visible {
						t.Fatal("late start marker")
					}
					started = true
				}
				if d.Content != "" {
					visible = true
					text += d.Content
				}
				if d.Usage != nil {
					usage = d.Usage
				}
			}
			if started == isOpenCodeZenBaseURL(base) {
				t.Fatalf("reasoning timing marker=%v base=%s", started, base)
			}
			if text != "OK" || usage == nil || usage.Reasoning != 151 {
				t.Fatalf("text=%q usage=%+v", text, usage)
			}
		})
	}
}
