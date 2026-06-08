package llm

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// EchoProvider is the noop provider used in tests and offline mode.
// It returns the last user message verbatim, prefixed with the
// provider name, sent as two streaming deltas with a tiny pause in
// between so consumers can verify their streaming wiring.
type EchoProvider struct {
	name    string
	hasVision bool
}

// NewEcho returns an EchoProvider. Empty name is rejected.
func NewEcho(name string) (*EchoProvider, error) {
	if name == "" {
		return nil, fmt.Errorf("llm.NewEcho: name is empty")
	}
	return &EchoProvider{name: name}, nil
}

// SetVision toggles vision-capability flag. Used by tests to verify
// the agent loop honours the capability flag.
func (e *EchoProvider) SetVision(v bool) { e.hasVision = v }

// Name implements Provider.
func (e *EchoProvider) Name() string { return e.name }

// SupportsVision implements VisionAware.
func (e *EchoProvider) SupportsVision() bool { return e.hasVision }

// Complete implements Provider. It validates the messages, finds
// the last user message, and streams the echo in three deltas:
// "[echo:<name>] " (text), <message body> (text), finish "stop".
//
// For tool messages, the provider echoes the tool name + body.
// For multimodal messages with vision enabled, the same path runs;
// without vision, images are stripped via TextOnly() with a stub
// marker.
func (e *EchoProvider) Complete(ctx context.Context, msgs []Message, _ []ToolDef) (<-chan Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("Complete: no messages")
	}
	for i, m := range msgs {
		if err := m.Validate(); err != nil {
			return nil, fmt.Errorf("Complete: message %d: %w", i, err)
		}
	}

	// Pick what to echo. Prefer last user; fall back to last.
	pick := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == RoleUser {
			pick = i
			break
		}
	}
	if pick < 0 {
		pick = len(msgs) - 1
	}
	target := msgs[pick]

	out := make(chan Delta, 4)
	go func() {
		defer close(out)
		defer func() {
			if r := recover(); r != nil {
				select {
				case out <- Delta{Err: fmt.Errorf("echo panic: %v", r)}:
				default:
				}
			}
		}()
		// If vision is disabled but the message has images, strip them.
		if !e.hasVision && target.HasImage() {
			textOnly := target.TextOnly()
			target = textOnly
		}

		var body string
		switch target.Role {
		case RoleUser:
			body = target.Content
			if body == "" {
				// compose from text parts
				var b strings.Builder
				for _, p := range target.Parts {
					if p.Type == PartTypeText {
						b.WriteString(p.Text)
					}
				}
				body = b.String()
			}
		case RoleTool:
			body = fmt.Sprintf("[tool:%s] %s", target.Name, target.Content)
		default:
			body = target.Content
		}

		prefix := fmt.Sprintf("[echo:%s] ", e.name)

		// Initial delta with role.
		select {
		case out <- Delta{Role: RoleAssistant}:
		case <-ctx.Done():
			return
		}

		// First text chunk.
		select {
		case out <- Delta{Content: prefix}:
		case <-ctx.Done():
			return
		}

		// Tiny pause so streaming behaviour is observable in tests.
		select {
		case <-time.After(2 * time.Millisecond):
		case <-ctx.Done():
			return
		}

		// Body chunk.
		select {
		case out <- Delta{Content: body}:
		case <-ctx.Done():
			return
		}

		// Terminal delta.
		select {
		case out <- Delta{FinishReason: "stop", Usage: &Usage{Input: 0, Output: 0, Total: 0}}:
		case <-ctx.Done():
			return
		}
	}()
	return out, nil
}
