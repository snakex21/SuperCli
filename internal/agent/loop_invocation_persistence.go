package agent

import "supercli/internal/tools"

type invocationPersistence struct {
	factory func(SessionWriter) SessionWriter
	receipt func(SessionWriter) (seq int, messageID int64)
	bind    func(seq int, messageID int64)
}

// SetInvocationPersistence configures per-Run writer ownership without a
// dependency on the concrete storage package. Configure it before running the
// loop. Factory must return a fresh writer for the current writer's session,
// or nil to leave the current binding unchanged and skip receipt notification.
// Receipt reads the new writer's first committed prompt; bind runs only after
// that exact prompt was written, synchronously before the model goroutine.
// Neither factory nor bind is called for an empty, busy or rejected Run.
func (l *Loop) SetInvocationPersistence(factory func(SessionWriter) SessionWriter, receipt func(SessionWriter) (int, int64), bind func(int, int64)) {
	l.invocationPersistence = invocationPersistence{factory: factory, receipt: receipt, bind: bind}
}

func (l *Loop) beginInvocationPersistence() bool {
	if l.invocationPersistence.factory == nil {
		return false
	}
	writer := l.invocationPersistence.factory(l.writer)
	if writer == nil {
		return false
	}
	l.writer = writer
	if l.toolOutputsFollowWriter {
		l.toolOutputs, _ = writer.(tools.OutputPersistence)
	}
	return true
}

func (l *Loop) bindInvocationPrompt() {
	persistence := &l.invocationPersistence
	if persistence.receipt == nil || persistence.bind == nil {
		return
	}
	seq, messageID := persistence.receipt(l.writer)
	if seq > 0 && messageID > 0 {
		persistence.bind(seq, messageID)
	}
}
