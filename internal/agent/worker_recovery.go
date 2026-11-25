package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"supercli/internal/tools/core"
)

// Keep the recovery handle ahead of a partial report. The generic tool-error
// formatter keeps only a tail, which can drop a failed worker's identity and
// make its still-live context effectively undiscoverable to the coordinator.
func workerFailureHandoff(w *Worker, report string, cause error, observation string) error {
	s := w.Snapshot()
	return workerFailureHandoffFromSnapshot(w, s, report, cause, observation)
}

func workerFailureHandoffFromSnapshot(w *Worker, s Snapshot, report string, cause error, observation string) error {
	return workerFailureHandoffCaptured(s, workerCanContinue(w, s), report, cause, observation)
}

func workerCanContinue(w *Worker, s Snapshot) bool {
	canContinue := s.Status == "failed" && w.Loop != nil
	if canContinue {
		if budget, ok := w.Loop.creditTracker.(*tokenBudget); ok {
			used, _ := budget.Used()
			canContinue = budget.SessionCap() <= 0 || used <= budget.SessionCap()
		}
	}
	return canContinue
}

func workerFailureHandoffCaptured(s Snapshot, canContinue bool, report string, cause error, observation string) error {
	var b strings.Builder
	// A short reason, never a provider's raw JSON body; a usage limit says
	// when it resets.
	reason := shortProviderError(cause.Error())
	if until, ok := usageLimitReset(cause.Error(), time.Now()); ok {
		reason += "; usage limit resets at " + until.Local().Format("2006-01-02 15:04")
	}
	fmt.Fprintf(&b, "worker %s (%s), status=%s; call failed: %s",
		s.ID, s.Agent, s.Status, core.HeadTail(reason, 384, 128))
	if title := workerTaskTitle(s.Description); title != "" {
		fmt.Fprintf(&b, "\nTask: %s", title)
	}
	if canContinue && !errors.Is(cause, context.Canceled) {
		fmt.Fprintf(&b, "\nContext retained; send_message to %s continues this worker.", s.ID)
	}
	if report = strings.TrimSpace(report); report != "" && !strings.Contains(report, cause.Error()) && !strings.Contains(cause.Error(), report) {
		// (A "partial report" that only repeats the error is left out.)
		b.WriteString("\nPartial report (incomplete):\n")
		b.WriteString(core.HeadTail(report, 768, 256))
	}
	b.WriteString(observation) // already bounded by workerEvidenceInlineBytes
	return core.SelfContainedErr(&workerHandoffError{cause: cause, text: b.String()})
}

type workerHandoffError struct {
	cause error
	text  string
}

func (e *workerHandoffError) Error() string { return e.text }
func (e *workerHandoffError) Unwrap() error { return e.cause }
