package agent

import "context"

// workerInvocation belongs to one parent tool call, not to the worker's
// lifetime. A resumed worker must report to the current run/UI stream.
type workerInvocation struct {
	callID string
	out    chan<- Event
	checks func(verificationObservation)
}
type workerInvocationKey struct{}

func withWorkerInvocation(ctx context.Context, callID string, out chan<- Event, checks ...func(verificationObservation)) context.Context {
	invocation := workerInvocation{callID: callID, out: out}
	if len(checks) > 0 {
		invocation.checks = checks[0]
	}
	return context.WithValue(ctx, workerInvocationKey{}, invocation)
}

func workerProgressSink(ctx context.Context, w *Worker, run int) func(WorkerProgressEvent) {
	invocation, current := ctx.Value(workerInvocationKey{}).(workerInvocation)
	return func(ev WorkerProgressEvent) {
		ev.TaskID, ev.Agent, ev.Run = w.ID, w.Agent, run
		if current && invocation.out != nil {
			ev.ParentCallID = invocation.callID
			select {
			case invocation.out <- ev:
			case <-ctx.Done():
				// The originating UI may no longer consume events after cancel.
				// Preserve terminal receipts through the worker's lifetime sink.
				w.emitProgress(ev)
			}
			return
		}
		w.emitProgress(ev)
	}
}
