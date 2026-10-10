package session

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"supercli/internal/llm"
)

// Writer adapts a Store to the agent.SessionWriter interface
// for a specific session. The sessionID is captured at
// construction time so AppendMessage knows where to write.
type Writer struct {
	store            *Store
	sessionID        string
	userAppendMu     sync.Mutex
	firstUserReceipt atomic.Pointer[MessageReceipt]
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
	if msg.Role == llm.RoleUser {
		// Serialize user inserts for this invocation so a later concurrent
		// insert cannot publish its sequence before the first committed one.
		w.userAppendMu.Lock()
		defer w.userAppendMu.Unlock()
	}
	receipt, err := w.store.AppendMessageWithReceipt(ctx, w.sessionID, enc)
	if err != nil {
		return err
	}
	if msg.Role == llm.RoleUser && w.firstUserReceipt.Load() == nil {
		// User inserts are serialized above; publish one immutable pair so
		// readers cannot observe a sequence and ID from different messages.
		w.firstUserReceipt.Store(&MessageReceipt{Seq: receipt.Seq, ID: receipt.ID})
	}
	return nil
}

// FirstUserSeq returns the first successfully committed user message written
// by this Writer invocation, or zero until one succeeds. Assistant/tool writes
// and later user notifications do not change it. New invocations need a fresh
// Writer even when they reuse the same session. The read performs no SQL.
func (w *Writer) FirstUserSeq() int { return w.FirstUserReceipt().Seq }

// FirstUserReceipt returns a copy of this invocation's first successfully
// committed user insert, or the zero receipt until one succeeds. Later user
// notifications do not replace it, even if a rewind removes the original row.
// New invocations need a fresh Writer. The read is atomic and performs no SQL.
func (w *Writer) FirstUserReceipt() MessageReceipt {
	if receipt := w.firstUserReceipt.Load(); receipt != nil {
		return *receipt
	}
	return MessageReceipt{}
}

// SaveContextProjection persists the loop's provider-visible view while
// leaving the full transcript intact.
func (w *Writer) SaveContextProjection(ctx context.Context, msgs []llm.Message) error {
	return w.store.SaveContextProjection(ctx, w.sessionID, msgs)
}

// UpdateUsage records per-turn token counters on the session.
func (w *Writer) UpdateUsage(in, out int) error {
	return w.store.UpdateUsage(w.sessionID, in, out)
}

// UpdateUsageContext records counters within the turn completion deadline.
func (w *Writer) UpdateUsageContext(ctx context.Context, in, out int) error {
	return w.store.UpdateUsageContext(ctx, w.sessionID, in, out)
}

// TryUpdateUsage preserves counters after Stop without waiting for another DB writer.
func (w *Writer) TryUpdateUsage(ctx context.Context, in, out int) error {
	return w.store.TryUpdateUsage(ctx, w.sessionID, in, out)
}

// SessionID returns the session id the writer is bound to.
func (w *Writer) SessionID() string { return w.sessionID }
