package interactive

import "context"

type askChannelKey struct{}

// WithAskChannel routes questions to the current UI run. Retained workers
// may own tools created by a previous HTTP request whose channel is closed
// to consumers. Context routing preserves their conversation without stale UI.
func WithAskChannel(ctx context.Context, ch chan<- AskRequest) context.Context {
	return context.WithValue(ctx, askChannelKey{}, ch)
}

type durableAskChannelKey struct{}

// WithDurableAskChannel names the conversation-level question route. A
// detached (background) worker outlives the UI run that started it; its
// questions use this route instead of that run's channel.
func WithDurableAskChannel(ctx context.Context, ch chan<- AskRequest) context.Context {
	return context.WithValue(ctx, durableAskChannelKey{}, ch)
}

// DurableAskChannel returns the conversation-level question route, or nil.
func DurableAskChannel(ctx context.Context) chan<- AskRequest {
	if ctx == nil {
		return nil
	}
	ch, _ := ctx.Value(durableAskChannelKey{}).(chan<- AskRequest)
	return ch
}
