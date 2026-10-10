package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

type workerHandoffError struct {
	cause error
	text  string
}

func (e *workerHandoffError) Error() string { return e.text }
func (e *workerHandoffError) Unwrap() error { return e.cause }
