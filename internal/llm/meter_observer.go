package llm

import "context"

// RequestObserver sees each metered request before it is sent. It is a
// read-only measurement hook (the batch --trace-jsonl context report); it
// must not modify msgs or tools and must be safe for concurrent use.
type RequestObserver func(purpose string, msgs []Message, tools []ToolDef)

type requestObserverCtxKey struct{}

// WithRequestObserver attaches obs to ctx; every metered Complete under ctx
// calls it with the resolved purpose. Nil obs leaves ctx unchanged.
func WithRequestObserver(ctx context.Context, obs RequestObserver) context.Context {
	if obs == nil || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, requestObserverCtxKey{}, obs)
}

func requestObserverFromContext(ctx context.Context) RequestObserver {
	if ctx == nil {
		return nil
	}
	obs, _ := ctx.Value(requestObserverCtxKey{}).(RequestObserver)
	return obs
}
