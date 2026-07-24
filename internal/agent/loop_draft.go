package agent

import (
	"context"
	"strings"
	"time"

	"supercli/internal/llm"
)

// invokeDraft asks the draft bridge for a plan and
// injects it as a system message. Records the draft
// output tokens for later savings computation. A
// failure (empty plan, context cancel, provider
// error) is logged via silent no-op — drafts are
// best-effort and must never abort the verifier
// run.
func (l *Loop) invokeDraft(ctx context.Context, step int, _ chan<- Event) {
	if l.draftBridge == nil {
		return
	}
	// Build the draft input: the most-recent user
	// message PLUS a trimmed slice of recent
	// conversation (last few messages, tool outputs
	// truncated hard) so the draft model is actually
	// informed about what already happened, while
	// keeping token cost low — cheap drafts are the
	// whole point of F11.
	prompt := l.draftPrompt()
	if prompt == "" {
		return
	}
	draftStart := time.Now()
	res, err := l.draftBridge.Plan(llm.WithPurpose(ctx, llm.PurposeDraft), prompt)
	l.recordAuxWall(llm.PurposeDraft, time.Since(draftStart))
	if err != nil || strings.TrimSpace(res.Text) == "" {
		// Silent no-op: a draft failure must not
		// break the run. Reset state so the
		// post-verifier outcome step sees a clean
		// slate.
		l.lastDraftText = ""
		l.lastDraftTokens = 0
		return
	}
	sysMsg := l.draftBridge.AsSystemMessage(res.Text)
	l.Messages = append(l.Messages, sysMsg)
	l.persist(ctx, sysMsg)
	l.lastDraftText = res.Text
	l.lastDraftTokens = res.Tokens
	l.draftPolicy.MarkDrafted(step)
}

// lastUserPrompt returns the most recent user-role
// message content. Used by the draft bridge as the
// "what to plan for" input.
func (l *Loop) lastUserPrompt() string {
	for i := len(l.Messages) - 1; i >= 0; i-- {
		if l.Messages[i].Role == llm.RoleUser {
			return l.Messages[i].Content
		}
	}
	return ""
}

// Draft-context trimming bounds. The draft model gets the
// last draftContextMessages messages of conversation; tool
// results are truncated to draftToolOutputCap characters and
// other messages to draftMessageCap. Cheap and informed beats
// cheap and blind.
const (
	draftContextMessages = 6
	draftToolOutputCap   = 300
	draftMessageCap      = 600
)

// draftPrompt builds the input for the draft model: a trimmed
// view of the recent conversation followed by the current user
// request. Returns "" when there is no user message at all
// (nothing to plan for).
func (l *Loop) draftPrompt() string {
	userPrompt := l.lastUserPrompt()
	if userPrompt == "" {
		return ""
	}
	// Collect the last N messages (excluding system
	// messages — draft plans and reflections would only
	// add noise) in chronological order.
	var recent []string
	count := 0
	for i := len(l.Messages) - 1; i >= 0 && count < draftContextMessages; i-- {
		m := l.Messages[i]
		if m.Role == llm.RoleSystem {
			continue
		}
		text := messageDraftText(m)
		if text == "" {
			continue
		}
		limit := draftMessageCap
		label := string(m.Role)
		if m.Role == llm.RoleTool {
			limit = draftToolOutputCap
			label = "tool result"
		}
		if len(text) > limit {
			text = text[:limit] + " ...[truncated]"
		}
		recent = append([]string{label + ": " + text}, recent...)
		count++
	}
	if len(recent) <= 1 {
		// Nothing beyond the user prompt itself: keep
		// the old cheap behavior.
		return userPrompt
	}
	var sb strings.Builder
	sb.WriteString("Recent conversation (oldest first, tool outputs truncated):\n")
	for _, r := range recent {
		sb.WriteString(r)
		sb.WriteString("\n")
	}
	sb.WriteString("\nCurrent request: ")
	sb.WriteString(userPrompt)
	return sb.String()
}

// messageDraftText extracts a plain-text rendering of a
// message for the draft context: Content, text parts, and
// tool-call names (so the draft sees WHICH tools ran even
// when the assistant message had no prose).
func messageDraftText(m llm.Message) string {
	var sb strings.Builder
	if m.Content != "" {
		sb.WriteString(m.Content)
	}
	for _, p := range m.Parts {
		if p.Type == llm.PartTypeText && p.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(p.Text)
		}
	}
	if len(m.ToolCalls) > 0 {
		var names []string
		for _, tc := range m.ToolCalls {
			names = append(names, tc.Name)
		}
		if sb.Len() > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString("[called tools: " + strings.Join(names, ", ") + "]")
	}
	return strings.TrimSpace(sb.String())
}

// recordDraftUsage charges the draft call's token
// usage to the credit tracker. Draft spend shares
// the F7 budget (D2 decision). Called once per draft
// turn, after the verifier's usage is recorded.
func (l *Loop) recordDraftUsage(ctx context.Context) error {
	if l.creditTracker == nil || l.draftBridge == nil {
		return nil
	}
	// Draft providers report input tokens too
	// (the system + user prompt). For now we
	// pass output as 0/0 because the bridge
	// captured only output tokens; the tracker
	// charges the verifier's total. We add a
	// second Record call with the draft's
	// (input, output) so the ledger shows the
	// draft spend. The total cost is the sum
	// of both records.
	draftModel := l.draftBridge.ModelName()
	// We don't have input tokens separately;
	// charge the output tokens as out. This
	// under-counts the draft's true cost
	// (input tokens) but keeps the ledger
	// honest about what we know. A future
	// enhancement would thread usage through
	// invokeDraft and store both halves.
	return l.creditTracker.Record(ctx, 0, int64(l.lastDraftTokens), draftModel)
}

// recordDraftOutcome computes the F11 savings /
// override signal. Emits DraftUsedEvent. Records
// savings to stats. If the verifier overrode the
// draft, writes a record to the override sink so
// the F5 reflector can learn from it.
func (l *Loop) recordDraftOutcome(step int, verifierText string, verifyTokens int, out chan<- Event) {
	if l.draftBridge == nil {
		return
	}
	draftText := l.lastDraftText
	draftTokens := l.lastDraftTokens
	savings, decision := l.draftSavings.Add(draftText, verifierText, draftTokens, verifyTokens)

	// Emit the event the TUI renders.
	out <- DraftUsedEvent{
		Step:          step,
		DraftModel:    l.draftBridge.ModelName(),
		VerifierModel: l.modelID,
		Decision:      decision,
		Savings:       savings,
	}

	// Feed the stats recorder.
	if l.stats != nil {
		l.stats.RecordSaved(savings)
	}

	// If the verifier overrode, record the pair
	// so the F5 reflector can learn from it.
	// Override detection: savings == 0 AND
	// decision == "overridden" (set by Savings.Add).
	if decision == "overridden" && l.draftOverrideSink != nil {
		_ = l.draftOverrideSink.RecordDraftOverride(ctxForOutcome(l), DraftOverride{
			Step:          step,
			DraftModel:    l.draftBridge.ModelName(),
			VerifierModel: l.modelID,
			DraftText:     draftText,
			VerifierText:  verifierText,
		})
	}

	// Clear last-draft state so the next
	// iteration (which may or may not have a
	// draft) starts fresh.
	l.lastDraftText = ""
	l.lastDraftTokens = 0
}

// ctxForOutcome returns a fresh background context
// for the override sink write. We deliberately
// don't pass the run's ctx because the sink is
// best-effort and must not be cancelled by user
// ctrl-c. Returns context.Background via a small
// helper to keep the call site tidy.
func ctxForOutcome(l *Loop) context.Context {
	return context.Background()
}
