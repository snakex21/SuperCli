package reflect

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"supercli/internal/llm"
)

// Reflector is the F5.a hook the agent loop calls every
// ReflectEvery steps. The implementation returns the
// reflection text that the loop will inject as a mid-
// conversation system message. Returning ("", nil) is a
// valid no-op (the loop should NOT inject an empty
// message).
type Reflector interface {
	Reflect(ctx context.Context, history []llm.Message) (string, error)
}

// ModelReflector is the default Reflector. It builds a
// small prompt and asks the provider for a 2-3 sentence
// self-review. The same provider is used; F5 ships with
// the same-model setup and F5b+1 will add a cheaper
// model via llm.Router.
type ModelReflector struct {
	Provider  llm.Provider
	MaxTokens int // default 200
	// HistoryTail is the maximum number of recent messages
	// to include in the reflection prompt. Older messages
	// are dropped. Default 5.
	HistoryTail int

	once    sync.Once
	prompt  string
	tail    int
	maxTok  int
}

// Prepare is called once at startup. It freezes the
// configured values and precomputes the system prompt
// string. Safe to call concurrently.
func (r *ModelReflector) Prepare() {
	r.once.Do(func() {
		r.prompt = reflectSystemPrompt
		r.tail = r.HistoryTail
		if r.tail <= 0 {
			r.tail = 5
		}
		r.maxTok = r.MaxTokens
		if r.maxTok <= 0 {
			r.maxTok = 200
		}
	})
}

// Reflect builds the reflection prompt and streams the
// provider's response. The full text is returned (the
// provider closes the channel exactly once; we read until
// close). Errors are returned as-is. The history is
// truncated to the last HistoryTail messages and stripped
// of any prior system messages (we don't want a chain of
// reflections confusing the reflector).
func (r *ModelReflector) Reflect(ctx context.Context, history []llm.Message) (string, error) {
	if r == nil || r.Provider == nil {
		return "", fmt.Errorf("reflect: ModelReflector not configured")
	}
	r.Prepare()
	transcript := r.transcript(history)
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: r.prompt},
		{Role: llm.RoleUser, Content: transcript},
	}
	ch, err := r.Provider.Complete(ctx, msgs, nil) // reflection doesn't need tools
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for d := range ch {
		if d.Err != nil {
			return strings.TrimSpace(b.String()), d.Err
		}
		b.WriteString(d.Content)
	}
	return strings.TrimSpace(b.String()), nil
}

// transcript renders the last N messages as plain text,
// stripping prior system messages. Each non-system message
// is rendered as "role: content" with newlines escaped.
func (r *ModelReflector) transcript(history []llm.Message) string {
	tail := history
	if len(tail) > r.tail {
		tail = tail[len(tail)-r.tail:]
	}
	var b strings.Builder
	for _, m := range tail {
		if m.Role == llm.RoleSystem {
			continue
		}
		fmt.Fprintf(&b, "[%s] %s\n", m.Role, oneLine(m.Content))
	}
	return strings.TrimRight(b.String(), "\n")
}

func oneLine(s string) string {
	// Collapse multi-line content into a single line so
	// the transcript stays readable. The full text is in
	// the model's context already; the reflection just
	// needs a summary.
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}

// reflectSystemPrompt is the system prompt used by
// ModelReflector. It is exported as a const so tests can
// reference it.
const reflectSystemPrompt = `You are a focused self-reviewer. Look at the recent conversation transcript and write 2-3 sentences covering:
1. What is working well so far?
2. What is not working or carries risk?
3. What should the agent try next?

Keep the answer under 80 words. Plain text, no markdown.`
