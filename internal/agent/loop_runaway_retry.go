package agent

import (
	"context"
	"errors"
	"fmt"

	"supercli/internal/llm"
)

// maxRunawayRetries bounds the re-requests per Run after a runaway tool call.
const maxRunawayRetries = 2

// retryRunawayToolCall handles a stream stopped by the runaway-arguments
// guard (llm.RunawayToolCallError). The call was cancelled while streaming
// and never executed, so asking again is safe. Before 1.0.28 the Run ended
// on the error with an empty answer; in the 1.0.28 eval runs (gpt-6-luna,
// effort low) that happened in about 1 % of runs, always on a large edit.
// The model gets one system note (send the call again, split a large edit)
// and the request is repeated, at most maxRunawayRetries times per Run.
func (l *Loop) retryRunawayToolCall(ctx context.Context, err error, out chan<- Event) bool {
	var runaway *llm.RunawayToolCallError
	if !errors.As(err, &runaway) || ctx.Err() != nil || l.runawayRetries >= maxRunawayRetries {
		return false
	}
	l.runawayRetries++
	what := "degenerated into whitespace while streaming its arguments"
	switch runaway.Reason {
	case llm.RunawayRepetition:
		what = "degenerated into a repeating block while streaming its arguments"
	case llm.RunawayTooLarge:
		what = "exceeded the tool-call argument size limit while streaming"
	case llm.RunawayTooSlow:
		what = "kept streaming its arguments past the time limit"
	}
	msg := llm.Message{Role: llm.RoleSystem, Content: fmt.Sprintf(
		"[runaway tool call] Your last %s call %s and was cancelled before it ran; nothing was changed. "+
			"Send the call again with complete, correct arguments; split a large edit into several smaller calls.", runaway.Tool, what)}
	l.Messages = append(l.Messages, msg)
	l.persist(ctx, msg)
	if out != nil {
		out <- NoticeEvent{Text: fmt.Sprintf("runaway %s call stopped (%s); asking the model to send it again (%d/%d)", runaway.Tool, runaway.Describe(), l.runawayRetries, maxRunawayRetries)}
	}
	return true
}
