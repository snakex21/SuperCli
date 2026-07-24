package app

import (
	"context"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/storage/goal"
)

// goalProviderAdapter bridges an llm.Provider (which
// returns a <-chan Delta) to the goal.Provider
// interface (which returns a single string). It drains
// the channel synchronously with a hard cap on tokens
// to avoid runaway cost from a misbehaving model.
//
// Used by tools.GoalTool.SetDecompose so the main
// agent's model can produce a task list for a goal.
// Tests inject their own stub goal.Provider to avoid
// the channel dance.
type goalProviderAdapter struct {
	Provider llm.Provider
}

func (a goalProviderAdapter) Complete(ctx context.Context, msgs []goal.Message) (string, error) {
	if a.Provider == nil {
		return "", nil
	}
	// Translate goal.Message to llm.Message.
	llmMsgs := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		llmMsgs[i] = llm.Message{Role: llm.Role(m.Role), Content: m.Content}
	}
	ch, err := a.Provider.Complete(llm.WithPurpose(ctx, llm.PurposeGoal), llmMsgs, nil) // goal decompose doesn't need tools
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for d := range ch {
		if d.Err != nil {
			return b.String(), d.Err
		}
		b.WriteString(d.Content)
		if b.Len() >= 32*1024 { // 32 KB sanity cap on the
			break // raw response before parsing
		}
	}
	return b.String(), nil
}
