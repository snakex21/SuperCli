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
