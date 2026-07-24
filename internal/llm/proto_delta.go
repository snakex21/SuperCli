package llm

import "fmt"

// Delta is a single streaming update from the model. The provider
// sends a sequence of Deltas over a channel; the consumer is
// expected to accumulate Content and ToolCall fragments into a
// final message.
//
// Exactly one of Content or ToolCall is typically set per Delta;
// the channel sends a final Delta with FinishReason set and no
// Content. On error, a single Delta{Err: ...} is sent and the
// channel is closed.
type Delta struct {
	// Role is the role of the message being streamed. It is set
	// on the FIRST delta of a turn and empty on subsequent deltas.
	Role Role

	// Content is a text fragment. Empty for deltas that only
	// carry tool calls or finish_reason.
	Content string

	// ToolCall is a fragment of a tool call. The provider may emit
	// multiple deltas per call (ID+Name on first, Arguments
	// accumulating on subsequent ones). The consumer is expected
	// to merge them by index.
	ToolCall *ToolCall

	// FinishReason is set on the final delta. Common values:
	//   "stop"           — model finished naturally
	//   "tool_calls"     — model wants to call a tool, loop again
	//   "length"         — truncated by max_tokens
	//   "content_filter" — blocked by safety filter
	FinishReason string

	// Usage is set on the final delta when the provider reports
	// token accounting. May be nil.
	Usage *Usage

	// Err terminates the stream. The provider must send exactly
	// one Delta with Err set (and nothing else) before closing
	// the channel on a stream error.
	Err error

	// Notice is an informational, NON-terminal status line for the
	// user ("rate limited, retrying in 2s…"). It is not assistant
	// content: consumers must surface it in the UI/log but never
	// append it to the conversation history. A Delta carrying a
	// Notice carries nothing else.
	Notice string
}

// ToolCall is a single tool invocation requested by the model. The
// fields are populated incrementally as Deltas arrive:
//
//   - First Delta: ID + Name set, Arguments ""
//   - Subsequent Deltas: Arguments accumulates
//
// Arguments is the raw JSON string the provider received; the
// consumer is expected to parse it before invoking the tool.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Usage reports token accounting for one turn.
type Usage struct {
	Input  int
	Output int
	Total  int

	// CachedInput is the portion of Input that the backend served
	// from a prompt/KV cache (OpenAI prompt_tokens_details.cached_tokens,
	// llama.cpp/LM Studio equivalents). Zero when the backend does not
	// report it. Used to surface a cache-hit ratio in the status line.
	CachedInput int

	// Reasoning is the number of Output tokens spent on hidden
	// chain-of-thought (OpenAI completion_tokens_details.reasoning_tokens;
	// LM Studio reports it for reasoning models). Zero when not reported.
	Reasoning int
}

// Validate returns nil if the delta is structurally sound.
func (d Delta) Validate() error {
	if d.Err != nil {
		return nil // error delta is always valid
	}
	if d.Content != "" && d.ToolCall != nil {
		return fmt.Errorf("llm.Delta: Content and ToolCall both set")
	}
	if d.Role != "" {
		switch d.Role {
		case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		default:
			return fmt.Errorf("llm.Delta: unknown role %q", d.Role)
		}
	}
	return nil
}

// IsTerminal reports whether the delta ends the stream.
func (d Delta) IsTerminal() bool {
	return d.FinishReason != "" || d.Err != nil
}
