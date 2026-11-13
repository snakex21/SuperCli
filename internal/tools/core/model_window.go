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
	readBatchMinBudget = 4 * 1024
	readBatchMaxBudget = 32 * 1024
)

// ReadBatchBudget is the byte budget of one batched file read (read_many):
// about 8% of the active model's context window at ~4 characters per
// token, within 4-32 KiB, shared equally by the read_many calls of one step
// (WithReadBatchShare). Without a known window it keeps the historical
// inline batch limit. A batch never trims inside a returned file to meet it;
// ranges that do not fit are named for a follow-up read instead.
func ReadBatchBudget(ctx context.Context) int {
	budget := ModelReadBatchInlineBytes
	if window := ModelWindow(ctx); window > 0 {
		budget = min(max(window*4*8/100, readBatchMinBudget), readBatchMaxBudget)
	}
	if ctx != nil {
		if calls, _ := ctx.Value(readBatchShareKey{}).(int); calls > 1 {
			budget = max(budget/calls, readBatchShareMin)
		}
	}
	return budget
}

type readBatchShareKey struct{}

// readBatchShareMin keeps a shared batch budget large enough for one
// ordinary source file's worth of lines.
const readBatchShareMin = 2 * 1024

// WithReadBatchShare records how many read_many calls one model step issued
// together. ReadBatchBudget is a per-step allowance: four parallel batches on
// a 16k model each taking the whole of it overflowed the window (eval
// long-inventory), so each call gets an equal share.
func WithReadBatchShare(ctx context.Context, calls int) context.Context {
	if ctx == nil || calls <= 1 {
		return ctx
	}
	return context.WithValue(ctx, readBatchShareKey{}, calls)
}
