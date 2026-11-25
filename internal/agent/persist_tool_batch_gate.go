package agent

import (
	"context"
	"sync"

	"supercli/internal/llm"
)

// Tool-batch ordering in the saved transcript (after 1.0.40). A loop persists
// an assistant message with tool calls, runs the calls, then persists their
// results. Background worker reports can be written to the same conversation
// meanwhile by another loop object (the GUI builds a loop per turn; a worker
// started in an earlier turn delivers through that turn's loop). The rows are
// ordered by insertion, so a report landed between a tool_use message and its
// results, and every later request of that conversation was rejected
// ("tool_use ids were found without tool_result blocks immediately after").
//
// While a loop has a tool batch open in a conversation, other loops' appends
// to that conversation wait and are written right after the batch closes.

type toolBatchGate struct {
	mu       sync.Mutex
	owner    *Loop
	deferred []func()
}

// toolBatchGates: one gate per saved conversation id.
var toolBatchGates sync.Map

func (l *Loop) toolBatchGate() *toolBatchGate {
	sid := ""
	if s, ok := l.writer.(interface{ SessionID() string }); ok {
		sid = s.SessionID()
	}
	if sid == "" {
		return nil
	}
	g, _ := toolBatchGates.LoadOrStore(sid, &toolBatchGate{})
	return g.(*toolBatchGate)
}

// openToolBatchGate is called before the assistant tool-call message is saved.
func (l *Loop) openToolBatchGate() {
	if g := l.toolBatchGate(); g != nil {
		g.mu.Lock()
		if g.owner == nil {
			g.owner = l
		}
		g.mu.Unlock()
	}
}

// closeToolBatchGate writes what other loops deferred, in arrival order.
func (l *Loop) closeToolBatchGate() {
	g := l.toolBatchGate()
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.owner != l {
		g.mu.Unlock()
		return
	}
	g.owner = nil
	deferred := g.deferred
	g.deferred = nil
	g.mu.Unlock()
	for _, write := range deferred {
		write()
	}
}

// deferWhileOtherBatchOpen queues msg when another loop has a tool batch open
// in this conversation and reports whether it did.
func (l *Loop) deferWhileOtherBatchOpen(ctx context.Context, msg llm.Message) bool {
	g := l.toolBatchGate()
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.owner == nil || g.owner == l {
		return false
	}
	ctx = context.WithoutCancel(ctx)
	g.deferred = append(g.deferred, func() { l.persistAppend(ctx, msg) })
	return true
}
