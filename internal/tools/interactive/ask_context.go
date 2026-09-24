package interactive

import "context"

type askChannelKey struct{}

// WithAskChannel routes questions to the current UI run. Retained workers
// may own tools created by a previous HTTP request whose channel is closed
// to consumers. Context routing preserves their conversation without stale UI.
func WithAskChannel(ctx context.Context, ch chan<- AskRequest) context.Context {
	return context.WithValue(ctx, askChannelKey{}, ch)
}
