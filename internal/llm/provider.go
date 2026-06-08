package llm

import "context"

// ToolDef is the minimal tool definition passed to the
// provider for inclusion in the API request. It mirrors
// the OpenAI function-calling format.
type ToolDef struct {
	Name        string
	Description string
	Schema      string // JSON Schema for the tool's parameters
}

// Provider is the contract every concrete LLM provider must satisfy.
//
// Complete kicks off a streaming completion. The returned channel is
// closed by the provider exactly once, after all deltas (including
// the terminal one) have been sent.
//
// Complete itself returns an error only when the request cannot
// even be initiated (e.g. bad config, network refused, context
// already cancelled). Once the channel is open, stream errors are
// delivered as Delta{Err: ...} and the channel is then closed.
//
// Implementations MUST be safe to call Complete on concurrently from
// different goroutines, but a single call MUST NOT be invoked
// concurrently with itself.
type Provider interface {
	Name() string
	Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error)
}
