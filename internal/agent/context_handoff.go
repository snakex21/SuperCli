package agent

import (
	"context"
	"strings"

	"supercli/internal/llm"
)

// A changed model cannot reuse its predecessor's prompt cache. Compact a large
// replay once, using the same policy for every transport and front-end. This
// is a trigger, not a hard cap: two recent user turns are always kept intact.
const modelHandoffTokens = 32_000

// contextModelStore records the model that actually consumed the conversation,
// independently of model-picker settings. Optional so ephemeral workers and
// existing SessionWriter implementations keep working.
type contextModelStore interface {
	ReadContextModel(context.Context) (provider, model string, err error)
	SaveContextModel(context.Context, string, string) error
}

type contextModelState struct {
	loaded    bool
	provider  string
	model     string
	attempted string
}

func (l *Loop) contextModelKey() string { return l.prefillScope() + "\x00" + l.modelID }

func (l *Loop) needsModelHandoff(ctx context.Context) bool {
	s := &l.contextModel
	if !s.loaded {
		s.loaded = true
		if w, ok := l.writer.(contextModelStore); ok {
			provider, model, err := w.ReadContextModel(ctx)
			if err != nil {
				// Metadata is an optimization; a failed read must not erase history.
				s.provider, s.model = l.prefillScope(), l.modelID
				return false
			}
			s.provider, s.model = provider, model
		}
	}
	if s.attempted == l.contextModelKey() {
		return false
	}
	if s.model != "" {
		changed := s.model != l.modelID || s.provider != l.prefillScope()
		if changed {
			l.lastThinking = ""
		}
		return changed
	}
	// Older sessions have no identity yet. Check once when resuming actual
	// conversation, never for a new session containing only a user prompt.
	for _, m := range l.Messages {
		if m.Role == llm.RoleAssistant {
			return true
		}
	}
	return false
}

func (l *Loop) rememberContextModel(ctx context.Context) {
	s := &l.contextModel
	provider := l.prefillScope()
	if s.loaded && s.provider == provider && s.model == l.modelID {
		return
	}
	s.loaded, s.provider, s.model = true, provider, l.modelID
	s.attempted = ""
	if w, ok := l.writer.(contextModelStore); ok {
		if err := w.SaveContextModel(ctx, provider, l.modelID); err != nil {
			// Keep the successful in-memory identity; a metadata failure does not
			// invalidate the transcript or require a model retry.
			l.persistNotify("could not save session model identity: " + err.Error())
		}
	}
}

func (l *Loop) resetModelContextBaseline() {
	l.sessUsageMu.Lock()
	l.contextBaseExact, l.contextBaseEstimated = 0, 0
	l.sessUsageMu.Unlock()
	l.lastThinking = ""
}

// maybeModelHandoff runs before ordinary window defense. It never hides
// messages after an unsuccessful summary and never summarizes the active turn.
func (l *Loop) maybeModelHandoff(ctx context.Context, out chan<- Event) bool {
	if !l.needsModelHandoff(ctx) || l.summarizer == nil {
		return false
	}
	window := l.windowResolution()
	hard := autoCompactThreshold(window.Tokens)
	threshold, source := l.effectiveCompactThreshold(hard, window.Source)
	if threshold > modelHandoffTokens {
		threshold, source = modelHandoffTokens, "model-handoff"
	}
	estimate := l.nextRequestTokenEstimate()
	if estimate.Effective <= threshold {
		return false
	}
	all := l.AllMessages()
	split := autoCompactSplit(all)
	if split <= leadingSystemCount(all) || !hasFreshCompactablePrefix(all, split) {
		return false
	}
	prefix := l.handoffPrefix(split)
	if !hasFreshCompactablePrefix(prefix, len(prefix)) {
		return false
	}
	l.contextModel.attempted = l.contextModelKey()
	if out != nil {
		select {
		case out <- NoticeEvent{Text: "Preparing a shorter context for this model…"}:
		case <-ctx.Done():
			return true
		}
	}
	summary, err := l.summarizePrefix(ctx, prefix)
	if err != nil || strings.TrimSpace(summary) == "" || !compactionReduces(prefix, summary) {
		return true
	}
	removed := l.CompactPrefixWithSummary(summary, split)
	if removed == 0 {
		return true
	}
	l.resetModelContextBaseline()
	if out != nil {
		select {
		case out <- AutoCompactEvent{
			Removed: removed, Window: window.Tokens, Estimated: estimate.Effective,
			RawEstimated: estimate.Raw, EstimateSource: estimate.Source, ExactBase: estimate.ExactBase,
			Threshold: threshold, ThresholdSource: source, WindowSource: window.Source, Reason: "model-switch",
		}:
		case <-ctx.Done():
		}
	}
	return true
}

// Exclude explicitly hidden history from summary input as well as main input.
func (l *Loop) handoffPrefix(split int) []llm.Message {
	if l.hidden == nil {
		return l.Messages[:split]
	}
	out := make([]llm.Message, 0, split)
	for i, msg := range l.Messages[:split] {
		if i < len(l.hidden) && l.hidden[i] {
			continue
		}
		out = append(out, msg)
	}
	return out
}
