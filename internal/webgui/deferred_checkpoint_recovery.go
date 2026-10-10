package webgui

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"supercli/internal/checkpoint"
	"supercli/internal/storage/session"
)

// Called on explicit transcript/page loading, using summaries already read for
// that page. Ordinary and resolved summaries cause no checkpoint metadata I/O.
func (e *Engine) recoverCheckpointChanges(ctx context.Context, store *session.Store, sid string, turns []session.TurnSummary) {
	var owners []checkpoint.CompletionIdentity
	var indexes map[string]int
	for i := range turns {
		b := turns[i].CheckpointBinding
		if b == nil || b.Resolved {
			continue
		}
		if indexes == nil {
			indexes = make(map[string]int)
		}
		if _, duplicate := indexes[b.Key]; duplicate {
			indexes[b.Key] = -1
			continue
		}
		indexes[b.Key] = i
		owners = append(owners, checkpoint.CompletionIdentity{Key: b.Key, SessionID: sid, UserSeq: b.UserSeq, UserMessageID: b.UserMessageID})
	}
	if len(owners) == 0 {
		return
	}
	manager, err := e.checkpointManager(e.Home())
	if err != nil {
		return
	}
	repairCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	err = manager.WithCompletionRecords(repairCtx, owners, func(owner checkpoint.CompletionIdentity, record checkpoint.Record) error {
		i := indexes[owner.Key]
		if i < 0 {
			return nil
		}
		turn := &turns[i]
		changes := make([]session.FileChange, 0, len(record.Changes))
		for _, change := range record.Changes {
			changes = append(changes, session.FileChange{Path: change.Path, Kind: change.Kind})
		}
		if err := store.ResolveCheckpointChanges(repairCtx, sid, turn.AssistantSeq, turn.RowID, *turn.CheckpointBinding, changes); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		turn.FileChanges = changes
		turn.CheckpointBinding.Resolved = true
		return nil
	})
	if err != nil && !errors.Is(err, checkpoint.ErrStoreBusy) && repairCtx.Err() == nil {
		log.Printf("checkpoint transcript changes: %v", err)
	}
}
