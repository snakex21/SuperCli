package agent

import (
	"context"
	"time"

	"supercli/internal/llm"
)

// recordStepUsage accounts for tokens already delivered even if the stream fails.
// Production session writes use a bounded context after cancellation; the original
// stream error remains the reason the turn ends.
func (l *Loop) recordStepUsage(ctx context.Context, totalUsage *Usage, usage *llm.Usage) error {
	if usage == nil {
		return nil
	}
	interrupted := ctx.Err() != nil
	if interrupted {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
	}
	totalUsage.Input += usage.Input
	totalUsage.Output += usage.Output
	totalUsage.Total += usage.Total
	totalUsage.Cached += usage.CachedInput
	totalUsage.Reasoning += usage.Reasoning
	l.sessUsageMu.Lock()
	l.sessUsage.Input += usage.Input
	l.sessUsage.Output += usage.Output
	l.sessUsage.Total += usage.Total
	l.sessUsage.Cached += usage.CachedInput
	l.sessUsage.Reasoning += usage.Reasoning
	// Last-turn snapshot for the status-line badges.
	l.lastTurnPrompt = usage.Input
	l.lastTurnCached = usage.CachedInput
	l.lastTurnOutput = usage.Output
	l.lastTurnReasoning = usage.Reasoning
	l.lastTurnSet = true
	l.sessUsageMu.Unlock()
	// Provider-reported prompt/completion tokens for the
	// phase telemetry (llama.cpp and the cloud backends
	// report usage on the final delta).
	if l.stats != nil {
		l.stats.RecordTokens(usage.Input, usage.Output)
		l.stats.RecordModel(l.modelID)
	}
	// Report per-turn usage to the writer (if any).
	// Failures feed the persistence-health tracker
	// (sticky first error + /status) but never abort
	// the run.
	if l.writer != nil {
		if err := l.updateUsage(ctx, usage.Input, usage.Output, interrupted); err != nil {
			l.persistUsageFailure(err)
		}
	}
	// F7: record to credit tracker. A budget
	// cap is a hard stop, but we still emit
	// the partial usage for the turn.
	if l.creditTracker != nil {
		if err := l.creditTracker.Record(ctx, int64(usage.Input), int64(usage.Output), l.modelID); err != nil {
			return err
		}
	}
	// F11: charge the draft call's tokens
	// against the same tracker. Draft spend
	// shares the user's F7 budget (per D2
	// decision). The draft provider's
	// usage is captured at the end of
	// invokeDraft.
	if l.draftSavings != nil && l.lastDraftTokens > 0 {
		if err := l.recordDraftUsage(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (l *Loop) updateUsage(ctx context.Context, in, out int, interrupted bool) error {
	if interrupted {
		if w, ok := l.writer.(interruptedUsageWriter); ok {
			return w.TryUpdateUsage(ctx, in, out)
		}
	}
	if w, ok := l.writer.(usageContextWriter); ok {
		return w.UpdateUsageContext(ctx, in, out)
	}
	return l.writer.UpdateUsage(in, out)
}
