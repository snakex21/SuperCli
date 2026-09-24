package agent

import "context"

// workerInvocation belongs to one parent tool call, not to the worker's
// lifetime. A resumed worker must report to the current run/UI stream.
type workerInvocation struct {
	callID string
	out    chan<- Event
}
type workerInvocationKey struct{}

func withWorkerInvocation(ctx context.Context, callID string, out chan<- Event) context.Context {
	return context.WithValue(ctx, workerInvocationKey{}, workerInvocation{callID, out})
}

func workerProgressSink(ctx context.Context, w *Worker, run int) func(WorkerProgressEvent) {
	invocation, current := ctx.Value(workerInvocationKey{}).(workerInvocation)
	return func(ev WorkerProgressEvent) {
		ev.TaskID, ev.Agent, ev.Run = w.ID, w.Agent, run
		if current {
			ev.ParentCallID = invocation.callID
			select {
			case invocation.out <- ev:
			case <-ctx.Done():
			}
			return
		}
		w.emitProgress(ev)
	}
}
