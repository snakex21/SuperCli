package core

import "context"

type modelWindowKey struct{}

// WithModelWindow records the active model's context window (in tokens) for
// one tool invocation. Tools that return page-sized text (web_fetch) size
// their default output from it so small local models (8k-32k contexts) are
// not flooded while large-context models still get a useful window.
func WithModelWindow(ctx context.Context, tokens int) context.Context {
	if ctx == nil || tokens <= 0 {
		return ctx
	}
	return context.WithValue(ctx, modelWindowKey{}, tokens)
}

// ModelWindow returns the context window recorded by WithModelWindow, or 0.
func ModelWindow(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	tokens, _ := ctx.Value(modelWindowKey{}).(int)
	return tokens
}

const (
	readBatchMinBudget = 8 * 1024
	readBatchMaxBudget = 32 * 1024
)

// ReadBatchBudget is the byte budget of one batched file read (read_many):
// about 15% of the active model's context window at ~4 characters per
// token, within 8-32 KiB. Without a known window it keeps the historical
// inline batch limit. A batch never trims inside a returned file to meet it;
// ranges that do not fit are named for a follow-up read instead.
func ReadBatchBudget(ctx context.Context) int {
	window := ModelWindow(ctx)
	if window <= 0 {
		return ModelReadBatchInlineBytes
	}
	return min(max(window*4*15/100, readBatchMinBudget), readBatchMaxBudget)
}
