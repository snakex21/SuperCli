package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

// Independent pre-change byte oracle, copied from the frozen source before the
// per-invocation handoff refactor. Formatting/evidence/recovery rules stay exact.
func legacyHandoffrenderWorkerNotification(w *Worker, result string) string {
	s := w.Snapshot()
	status := s.Status
	if status == "" {
		status = "done"
	}
	summary := legacyHandoffworkerSummary(w)
	toolsUsed := workerToolSummary(s.ToolNames)
	return fmt.Sprintf(`<task-notification>
<task-id>%s</task-id>
<agent>%s</agent>
<status>%s</status>
<summary>%s</summary>
<tools>%s</tools>
<result>%s</result>
</task-notification>`, s.ID, s.Agent, status, summary, toolsUsed, result)
}

func legacyHandoffworkerSummary(w *Worker) string {
	if w == nil {
		return "worker unknown"
	}
	s := w.Snapshot()
	status := s.Status
	if status == "" {
		status = "done"
	}
	// One-line status the coordinator can relay: kind, outcome, and the
	// resource cost (steps + tokens) so a run that hit a limit is legible.
	summary := fmt.Sprintf("%s %s · %d steps · %d in/%d out tok",
		s.Agent, status, s.Steps, s.TokensIn, s.TokensOut)
	// model-per-task telemetry: name the backend only when it differs
	// from the coordinator's (Model is set by the task tool then), so
	// the default single-model line keeps its historical format.
	if s.Model != "" {
		summary += " · model=" + s.Model
	}
	if s.LastError != "" {
		summary += ": " + s.LastError
		if strings.Contains(strings.ToLower(s.LastError), "max steps") {
			summary += fmt.Sprintf("; continue this worker with send_message to %s instead of starting over", s.ID)
		}
	}
	return summary
}

func legacyHandoffworkerResult(w *Worker, report string, err error) (result tools.Result) {
	notification := legacyHandoffrenderWorkerNotification(w, report)
	result = tools.Result{Text: notification, Err: err}
	var inlineObservation string
	if err != nil {
		defer func() { result.Err = legacyHandoffworkerFailureHandoff(w, report, err, inlineObservation) }()
	} else {
		defer func() {
			if len(result.Text) > core.ModelOutputInlineBytes {
				result.ModelPreview = legacyHandoffworkerReportPreview(w, report, result.Text[len(notification):])
			}
		}()
	}
	w.stateMu.RLock()
	evidence, run, updated, status := w.lastEvidence, w.Runs, w.UpdatedAt, w.Status
	w.stateMu.RUnlock()
	if evidence == "" || status == "running" {
		return result
	}
	observation := fmt.Sprintf(
		"\n\n[Worker %s run %d tool observations at %s; historical snapshots, not current workspace state; outputs may be truncated]\n",
		w.ID, run, updated.UTC().Format("2006-01-02T15:04:05Z")) + evidence
	// Tiny observations cost less than another retrieval round. Larger ones
	// stay off-context; neither case needs another worker inference.
	if len(observation) <= workerEvidenceInlineBytes {
		inlineObservation = observation
		result.Text += observation
		return result
	}
	result.RetainedText = result.Text + observation
	result.Text += "\n[Attached: worker tool observations (historical snapshots).]"
	return result
}

func legacyHandoffworkerReportPreview(w *Worker, report, suffix string) string {
	wrapper := legacyHandoffrenderWorkerNotification(w, "")
	if len(wrapper)+len(report)+len(suffix) <= core.ModelOutputInlineBytes {
		return ""
	}
	const outlineLabel = "Report headings (verbatim; omissions marked):\n"
	const excerptLabel = "\nReport excerpt:\n"
	budget := core.ModelOutputPreviewBytes - len(wrapper) - len(suffix) - len(outlineLabel) - len(excerptLabel)
	if budget < 1024 {
		return "" // Preserve the generic fallback for unusually large metadata.
	}
	outline, count := workerReportHeadings(report)
	if count < 3 {
		return ""
	}
	outline = workerReportClip(outline, budget/2)
	excerpt := workerReportClip(report, budget-len(outline))
	preview := legacyHandoffrenderWorkerNotification(w, outlineLabel+outline+excerptLabel+excerpt) + suffix
	if len(preview) > core.ModelOutputPreviewBytes {
		return ""
	}
	return preview
}

func legacyHandoffworkerFailureHandoff(w *Worker, report string, cause error, observation string) error {
	s := w.Snapshot()
	var b strings.Builder
	fmt.Fprintf(&b, "worker %s (%s), status=%s; call failed: %s",
		s.ID, s.Agent, s.Status, core.HeadTail(cause.Error(), 384, 128))
	canContinue := s.Status == "failed" && w.Loop != nil && !errors.Is(cause, context.Canceled)
	if canContinue {
		if budget, ok := w.Loop.creditTracker.(*tokenBudget); ok {
			used, _ := budget.Used()
			canContinue = budget.SessionCap() <= 0 || used <= budget.SessionCap()
		}
	}
	if canContinue {
		fmt.Fprintf(&b, "\nContext retained; send_message to %s continues this worker.", s.ID)
	}
	if report = strings.TrimSpace(report); report != "" {
		b.WriteString("\nPartial report (incomplete):\n")
		b.WriteString(core.HeadTail(report, 768, 256))
	}
	b.WriteString(observation) // already bounded by workerEvidenceInlineBytes
	return core.SelfContainedErr(&workerHandoffError{cause: cause, text: b.String()})
}
