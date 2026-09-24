package llm

import "strings"

// streamedToolCall owns one call's growing arguments. Keep it behind a pointer:
// strings.Builder must not be copied after its first write. Appending fragments
// is amortized linear rather than copying the entire prefix on every delta.
// The completed snapshot has ordinary immutable string arguments for consumers.
type streamedToolCall struct {
	ID        string
	Name      string
	arguments strings.Builder
}

func (call *streamedToolCall) snapshot() ToolCall {
	return ToolCall{ID: call.ID, Name: call.Name, Arguments: call.arguments.String()}
}
