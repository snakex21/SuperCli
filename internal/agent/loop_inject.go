package agent

import (
	"context"
	"fmt"
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

// InjectUserMessage queues an out-of-band user-role message. An active run
// receives it only after its complete assistant/tool exchange; an idle loop
// saves it immediately under the same ownership guard as Run/ResumeConversation.
func (l *Loop) InjectUserMessage(ctx context.Context, content string) {
	if l == nil || strings.TrimSpace(content) == "" {
		return
	}
	l.enqueueBackgroundMessage(backgroundMessage{content: content})
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
// with the next Run. Durable session writers snapshot them to session-media;
// pixels are active for one provider call, while later history keeps only the
// lightweight MediaID/file reference for explicit reload on demand.
func (l *Loop) SetNextUserImages(images []llm.ImageRef) {
	l.nextUserImages = append(l.nextUserImages[:0], images...)
}

// SetNextCoordinatorAddon queues text for the next coordinator-routed Run.
// Chat/advisor turns skip it without consuming it. Set before starting Run.
func (l *Loop) SetNextCoordinatorAddon(s string) {
	l.nextCoordinatorAddon = strings.TrimSpace(s)
	l.nextCoordinatorAddonSource = nil
}

// SetNextCoordinatorAddonSource collects one-shot context only when a Run
// needs the coordinator, including promotion after tool discovery. Chat/advisor
// turns do not invoke it. The source receives that Run's cancellation context;
// a canceled collection remains pending for a later Run. Set before Run.
func (l *Loop) SetNextCoordinatorAddonSource(source func(context.Context) string) {
	l.nextCoordinatorAddon = ""
	l.nextCoordinatorAddonSource = source
}

const maxPendingInterjections = 8

// pendingInterjection belongs to exactly one Run. A tracked entry gets a receipt
// through that Run's event stream; it never installs the sender's event sink.
type pendingInterjection struct {
	id   string
	text string
}

// QueueInterjection accepts a user message only while the current Run's inbox
// is open. False also covers cancellation and the final persistence/Done window,
// allowing the UI to keep its draft. Producers never mutate conversation history.
func (l *Loop) QueueInterjection(s string) bool {
	_, err := l.queueInterjection(s, false)
	return err == nil
}

func (l *Loop) queueInterjection(s string, tracked bool) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("steering instruction is empty")
	}
	l.interjectionMu.Lock()
	defer l.interjectionMu.Unlock()
	if !l.interjectionsOpen || l.interjectionCtx == nil || l.interjectionCtx.Err() != nil {
		return "", fmt.Errorf("the current run is no longer accepting steering; wait for it to finish, then use send_message without mode=steer")
	}
	if len(l.interjections) >= maxPendingInterjections {
		return "", fmt.Errorf("steering queue is full (%d pending instructions)", maxPendingInterjections)
	}
	id := ""
	if tracked {
		l.interjectionSeq++
		id = fmt.Sprintf("steer-%d", l.interjectionSeq)
	}
	l.interjections = append(l.interjections, pendingInterjection{id: id, text: s})
	return id, nil
}

func (l *Loop) openInterjections(ctx context.Context) {
	l.interjectionMu.Lock()
	l.interjectionCtx = ctx
	l.interjectionsOpen = ctx.Err() == nil
	l.interjectionMu.Unlock()
}

// With finish=true, observing an empty inbox and closing it is one atomic
// operation. A racing producer is either consumed by this Run or rejected;
// final SaveContextProjection cannot strand accepted instructions.
func (l *Loop) drainInterjections(ctx context.Context, out chan<- Event, canContinue, finish bool) int {
	l.interjectionMu.Lock()
	if ctx.Err() != nil || !canContinue {
		if finish {
			l.interjectionsOpen = false
		}
		l.interjectionMu.Unlock()
		return 0
	}
	pending := l.interjections
	l.interjections = nil
	if finish && len(pending) == 0 {
		l.interjectionsOpen = false
		l.interjectionCtx = nil
	}
	l.interjectionMu.Unlock()
	for _, item := range pending {
		msg := llm.Message{Role: llm.RoleUser, Content: item.text}
		l.Messages = append(l.Messages, msg)
		l.persist(ctx, msg)
		if item.id != "" {
			out <- steeringDeliveryEvent{ID: item.id, Text: item.text}
		}
	}
	if len(pending) > 0 {
		l.invalidateVisibleEstimate()
	}
	return len(pending)
}

// Run owns shutdown as well as delivery. Unconsumed instructions are explicitly
// rejected, never silently left for another Run. The transcript records the
// rejection as assistant text, not an executable future user instruction.
func (l *Loop) closeInterjections(ctx, saveCtx context.Context, out chan<- Event) {
	l.interjectionMu.Lock()
	l.interjectionsOpen = false
	l.interjectionCtx = nil
	pending := l.interjections
	l.interjections = nil
	l.interjectionMu.Unlock()
	if len(pending) == 0 {
		return
	}
	reason := "run ended before another steering delivery boundary (step or token limit, or run failure)"
	if err := ctx.Err(); err != nil {
		reason = "run cancelled before steering delivery: " + err.Error()
	}
	for _, item := range pending {
		notice := fmt.Sprintf("[Steering %s rejected: %s]\nUndelivered instruction: %q", item.id, reason, item.text)
		l.persist(saveCtx, llm.Message{Role: llm.RoleAssistant, Content: notice})
		if item.id != "" {
			out <- steeringDeliveryEvent{ID: item.id, Text: item.text, Err: fmt.Errorf("%s", reason)}
		} else {
			out <- NoticeEvent{Text: notice}
		}
	}
}

// CurrentModel returns the name of the active provider.
func (l *Loop) CurrentModel() string {
	return l.modelID
}

// SetModel swaps the provider and model ID at runtime.
// Used by /model hot-swap (F26.5). The capability
// check is the caller's responsibility.
func (l *Loop) SetModel(p llm.Provider) {
	if l.modelID != p.Name() {
		l.needsModelHandoff(context.Background())
		if l.contextModel.model == "" {
			l.contextModel.provider, l.contextModel.model = l.prefillScope(), l.modelID
		}
		l.resetModelContextBaseline()
	}
	l.provider = p
	l.modelID = p.Name()
}

// SetContextProvider updates the configured connection identity after a TUI
// hot-swap. It is separate from SetModel because llm.Provider.Name returns the
// model ID, not the user-defined connection/profile name.
func (l *Loop) SetContextProvider(provider string) {
	provider = strings.TrimSpace(provider)
	if l.contextProvider != provider {
		l.needsModelHandoff(context.Background())
		if l.contextModel.model == "" {
			l.contextModel.provider, l.contextModel.model = l.prefillScope(), l.modelID
		}
		l.resetModelContextBaseline()
	}
	l.contextProvider = provider
}

// ListModels returns all models from the capability registry.
func (l *Loop) ListModels() []llm.ModelInfo {
	if l.caps == nil {
		return nil
	}
	return l.caps.All()
}
