package session

import (
	"context"
	"fmt"

	"supercli/internal/llm"
)

// Writer adapts a Store to the agent.SessionWriter interface
// for a specific session. The sessionID is captured at
// construction time so AppendMessage knows where to write.
type Writer struct {
	store     *Store
	sessionID string
}

// NewWriter returns an agent.SessionWriter for the given
// session. Closing the session is the caller's responsibility.
func NewWriter(store *Store, sessionID string) *Writer {
	return &Writer{store: store, sessionID: sessionID}
}

// AppendMessage encodes msg and writes it as the next message
// in the session.
func (w *Writer) AppendMessage(ctx context.Context, msg llm.Message) error {
	enc, err := FromMessage(msg)
	if err != nil {
		return fmt.Errorf("session.Writer.AppendMessage: %w", err)
	}
	enc.SessionID = w.sessionID
	return w.store.AppendMessage(ctx, w.sessionID, enc)
}

// UpdateUsage records per-turn token counters on the session.
func (w *Writer) UpdateUsage(in, out int) error {
	return w.store.UpdateUsage(w.sessionID, in, out)
}

// SessionID returns the session id the writer is bound to.
func (w *Writer) SessionID() string { return w.sessionID }
