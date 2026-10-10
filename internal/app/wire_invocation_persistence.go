package app

import (
	"context"
	"supercli/internal/agent"
	"supercli/internal/checkpoint"
	"supercli/internal/storage/session"
)

// configureInvocationPersistence is shared by interactive and batch loops.
// It rotates only the writer; history, discovery, usage and buffered appends
// remain owned by the existing Loop. Resumed sessions follow the current SID.
func configureInvocationPersistence(loop *agent.Loop, store *session.Store, controller *checkpoint.Controller) {
	if loop == nil || store == nil {
		return
	}
	var bind func(int, int64)
	if controller != nil {
		controller.SetUserReceiptValidator(func(ctx context.Context, sid string, seq int, id int64) (bool, error) {
			return store.IsCurrentUserReceipt(ctx, sid, session.MessageReceipt{Seq: seq, ID: id})
		})
		bind = controller.SetUserMessageReceipt
	}
	loop.SetInvocationPersistence(func(current agent.SessionWriter) agent.SessionWriter {
		owner, ok := current.(interface{ SessionID() string })
		if !ok || owner.SessionID() == "" {
			return nil
		}
		return session.NewWriter(store, owner.SessionID())
	}, func(writer agent.SessionWriter) (int, int64) {
		if current, ok := writer.(*session.Writer); ok {
			receipt := current.FirstUserReceipt()
			return receipt.Seq, receipt.ID
		}
		return 0, 0
	}, bind)
}
