package agent

import (
	"context"

	"supercli/internal/llm"
)

// workerSelfKey carries the running worker in its tools' context.
type workerSelfKey struct{}

// WorkerRecallTranscript returns the calling worker's id and a copy of its own
// transcript, for session_recall (a worker searches its parent conversation
// and itself). ok is false outside a worker run.
func WorkerRecallTranscript(ctx context.Context) (string, []llm.Message, bool) {
	w, ok := ctx.Value(workerSelfKey{}).(*Worker)
	if !ok || w == nil || w.Loop == nil {
		return "", nil, false
	}
	return w.ID, append([]llm.Message(nil), w.Loop.Messages...), true
}
