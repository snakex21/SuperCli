package agent

import "context"

// WithUserInstructionsSnapshot is for frontends invoking delegated tools outside
// Loop.Run. Regular model/tool runs receive their snapshot automatically.
func WithUserInstructionsSnapshot(ctx context.Context, instructions string) context.Context {
	return context.WithValue(ctx, userInstructionsContextKey{}, instructions)
}
