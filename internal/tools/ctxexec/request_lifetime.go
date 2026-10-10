package ctxexec

import (
	"context"
)

// RequestLifetime lets checkpoint admission and command execution share one
// optional request budget. Zero preserves the caller context without a timer.
func RequestLifetime(ctx context.Context, timeoutMS int) (context.Context, context.CancelFunc, error) {
	request := &Request{Command: []string{"request-lifetime"}, TimeoutMS: timeoutMS}
	if err := request.Validate(); err != nil {
		return nil, nil, err
	}
	child, cancel := commandContext(ctx, timeoutMS)
	return child, cancel, nil
}
