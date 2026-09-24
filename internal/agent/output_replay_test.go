package agent

import (
	"context"
	"regexp"
	"strings"

	"supercli/internal/llm"
)

var outputHandlePattern = regexp.MustCompile(`handle=(out_[a-f0-9]+)`)

func handleInOutput(text string) string {
	match := outputHandlePattern.FindStringSubmatch(text)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

// Replays use a placeholder only in the test script; the provider copies the
// real reference from the preceding tool result, just as the model must do.
type outputReplayProvider struct {
	*stubProvider
	handle string
}

func (p *outputReplayProvider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	for _, msg := range msgs {
		if msg.Role == llm.RoleTool {
			if h := handleInOutput(msg.Content); h != "" {
				p.handle = h
			}
		}
	}
	if p.handle != "" && int(p.calls) < len(p.scripts) {
		for i, delta := range p.scripts[p.calls] {
			delta.Content = strings.ReplaceAll(delta.Content, "out_000001", p.handle)
			if delta.ToolCall != nil {
				call := *delta.ToolCall
				call.Arguments = strings.ReplaceAll(call.Arguments, "out_000001", p.handle)
				delta.ToolCall = &call
			}
			p.scripts[p.calls][i] = delta
		}
	}
	return p.stubProvider.Complete(ctx, msgs, defs)
}
