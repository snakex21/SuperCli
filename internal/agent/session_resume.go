package agent

import (
	"context"
	"fmt"

	"supercli/internal/llm"
)

// ResumeConversation switches history and its writer together while idle.
// Buffered writes must never leak into a different conversation.
func (l *Loop) ResumeConversation(ctx context.Context, writer SessionWriter, msgs []llm.Message, discovered []string) error {
	if !l.sessionBusy.CompareAndSwap(false, true) {
		return fmt.Errorf("agent is still finishing the previous run")
	}
	defer l.sessionBusy.Store(false)
	if err := ctx.Err(); err != nil {
		return err
	}
	if writer == nil {
		return fmt.Errorf("session writer unavailable")
	}
	state := l.PersistStatus()
	if state.Pending > 0 || state.ProjectionDirty {
		return fmt.Errorf("save the pending session history before switching conversations")
	}
	for len(msgs) > 0 && msgs[0].Role == llm.RoleSystem {
		msgs = msgs[1:]
	}
	if len(msgs) == 0 {
		return fmt.Errorf("session has no conversation messages")
	}
	l.LoadConversation(msgs)
	l.writer = writer
	l.contextModel = contextModelState{} // Read the resumed session's model identity.
	l.toolDiscovery = toolDiscoveryState{}
	if l.registry != nil {
		l.registry.ResetVisibility()
	}
	l.RestoreDiscoveredTools(discovered)
	return nil
}

func (l *Loop) SessionID() string {
	if w, ok := l.writer.(interface{ SessionID() string }); ok {
		return w.SessionID()
	}
	return ""
}
