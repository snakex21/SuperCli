package agent

import (
	"context"
	"strings"

	"supercli/internal/llm"
)

// SetExternalSink registers a channel that the
// loop can use to surface events that are NOT
// tied to a single Run — for example, F12
// ConsultEvent markers triggered by the /council
// slash command or the consult tool while the
// model is mid-turn. The channel is owned by the
// caller (typically the TUI's external-event
// pump); the loop never closes it. nil clears
// the sink. The loop holds no lock on the field
// — SetExternalSink must be called before any
// goroutine that may Emit.
func (l *Loop) SetExternalSink(ch chan<- Event) {
	l.extOut = ch
}

// Emit sends an event to the external sink if
// one is set. Non-blocking: when no sink is
// registered, when the sink is full, or when the
// caller is racing with a SetExternalSink(nil),
// the event is silently dropped. This matches
// the F2 design: external events are
// best-effort markers; losing one is preferable
// to blocking the tool layer.
//
// Returns true when the event was accepted, false
// otherwise. Currently informational; the only
// caller (the consult tool's OnResult) ignores it.
func (l *Loop) Emit(ev Event) bool {
	if l.extOut == nil {
		return false
	}
	select {
	case l.extOut <- ev:
		return true
	default:
		return false
	}
}

// InjectUserMessage appends an out-of-band user-role message to the loop. It is
// used by background workers to deliver task notifications to the coordinator's
// future context without requiring the user to paste them manually.
func (l *Loop) InjectUserMessage(ctx context.Context, content string) {
	if l == nil || strings.TrimSpace(content) == "" {
		return
	}
	msg := llm.Message{Role: llm.RoleUser, Content: content}
	l.Messages = append(l.Messages, msg)
	l.persist(ctx, msg)
}

// SetNextUserAddon queues text that will be appended once to the
// NEXT Run's user message, then cleared. It must be called before
// that Run starts (same goroutine discipline as SetExternalSink).
// The addon lands on the variable side of the prompt (a user
// message), never in the system prefix — KV-cache-prefix safe.
func (l *Loop) SetNextUserAddon(s string) {
	l.nextUserAddon = strings.TrimSpace(s)
}

// SetNextUserImages queues normalized images for direct multimodal delivery
// with the next Run. This avoids the legacy path -> read_image -> second model
// call round trip. Pixels are not persisted and are removed after the Run.
func (l *Loop) SetNextUserImages(images []llm.ImageRef) {
	l.nextUserImages = append(l.nextUserImages[:0], images...)
}

// SetNextCoordinatorAddon queues text for the next coordinator-routed Run.
// Chat/advisor turns skip it without consuming it. This is the preferred API
// for automatically collected repository context.
func (l *Loop) SetNextCoordinatorAddon(s string) {
	l.nextCoordinatorAddon = strings.TrimSpace(s)
}

const maxPendingInterjections = 8

// QueueInterjection accepts a user message while Run is active. It performs no
// model call and never mutates Messages from the caller goroutine; run() drains
// it at the next assistant/tool boundary. False means empty input or a full
// queue, allowing the UI to keep the draft for retry.
func (l *Loop) QueueInterjection(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	l.interjectionMu.Lock()
	defer l.interjectionMu.Unlock()
	if len(l.interjections) >= maxPendingInterjections {
		return false
	}
	l.interjections = append(l.interjections, s)
	return true
}

func (l *Loop) drainInterjections(ctx context.Context) int {
	l.interjectionMu.Lock()
	pending := append([]string(nil), l.interjections...)
	l.interjections = l.interjections[:0]
	l.interjectionMu.Unlock()
	for _, text := range pending {
		msg := llm.Message{Role: llm.RoleUser, Content: text}
		l.Messages = append(l.Messages, msg)
		l.persist(ctx, msg)
	}
	if len(pending) > 0 {
		l.invalidateVisibleEstimate()
	}
	return len(pending)
}

// CurrentModel returns the name of the active provider.
func (l *Loop) CurrentModel() string {
	return l.modelID
}

// SetModel swaps the provider and model ID at runtime.
// Used by /model hot-swap (F26.5). The capability
// check is the caller's responsibility.
func (l *Loop) SetModel(p llm.Provider) {
	l.provider = p
	l.modelID = p.Name()
}

// SetContextProvider updates the configured connection identity after a TUI
// hot-swap. It is separate from SetModel because llm.Provider.Name returns the
// model ID, not the user-defined connection/profile name.
func (l *Loop) SetContextProvider(provider string) {
	l.contextProvider = strings.TrimSpace(provider)
}

// ListModels returns all models from the capability registry.
func (l *Loop) ListModels() []llm.ModelInfo {
	if l.caps == nil {
		return nil
	}
	return l.caps.All()
}
