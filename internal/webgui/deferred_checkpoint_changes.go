package webgui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"supercli/internal/checkpoint"
	"supercli/internal/storage/session"
	"sync"
	"time"
)

// This sink outlives an SSE response but retains no emitter, Loop or provider.
// The two events may arrive in either order: summary commit and worker drain.
// Normal inline completion never publishes here and performs no extra SQL.
type deferredCheckpointChanges struct {
	mu                 sync.Mutex
	store              *session.Store
	sessionID          string
	userSeq            int
	userMessageID      int64
	manager            *checkpoint.Manager
	binding            *session.CheckpointBinding
	recordKey          string
	assistantSeq       int
	summaryID          int64
	recordID           string
	attempts           int
	changes            []session.FileChange
	published, flushed bool
}

func (d *deferredCheckpointChanges) setUserSeq(seq int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.userSeq = seq
}
func (d *deferredCheckpointChanges) setUserReceipt(seq int, id int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.userSeq, d.userMessageID = seq, id
}

func (d *deferredCheckpointChanges) bindCompletion(key string) *session.CheckpointBinding {
	d.mu.Lock()
	defer d.mu.Unlock()
	if key == "" || d.userSeq <= 0 || d.userMessageID <= 0 {
		return nil
	}
	if d.binding == nil {
		d.binding = &session.CheckpointBinding{Key: key, UserSeq: d.userSeq, UserMessageID: d.userMessageID}
	}
	if d.binding.Key != key {
		return nil
	}
	d.flushLocked()
	copy := *d.binding
	return &copy
}

func (d *deferredCheckpointChanges) complete(record *checkpoint.Record, err error) {
	if record != nil {
		d.publish(record)
		return
	}
	if err != nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.published {
		d.published = true
	} // Drained no-op, not a failed capture.
	d.flushLocked()
}

func (d *deferredCheckpointChanges) publish(record *checkpoint.Record) {
	if record == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.store == nil || d.userSeq <= 0 || record.SessionID != d.sessionID || record.UserSeq != d.userSeq || (d.userMessageID > 0 && record.UserMessageID != d.userMessageID) {
		return
	}
	if d.published {
		if record.ID != d.recordID {
			return
		}
		d.flushLocked()
		return
	}
	d.changes = make([]session.FileChange, 0, len(record.Changes))
	for _, change := range record.Changes {
		d.changes = append(d.changes, session.FileChange{Path: change.Path, Kind: change.Kind})
	}
	d.recordKey = record.CompletionKey
	d.recordID = record.ID
	d.published = true
	d.flushLocked()
}
func (d *deferredCheckpointChanges) bindSummary(seq int, rowID int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if seq <= 0 || rowID <= 0 || (d.assistantSeq != 0 && (d.assistantSeq != seq || d.summaryID != rowID)) {
		return
	}
	d.assistantSeq = seq
	d.summaryID = rowID
	d.flushLocked()
}
func (d *deferredCheckpointChanges) flushLocked() {
	if !d.published || d.flushed || d.assistantSeq == 0 || d.attempts >= 2 {
		return
	}
	if d.userMessageID > 0 && d.binding == nil {
		return
	}
	d.attempts++
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var err error
	if d.binding != nil {
		if d.manager == nil || (d.recordID != "" && d.recordKey != d.binding.Key) {
			return
		}
		b := *d.binding
		err = d.manager.WithUserReceipt(ctx, checkpoint.CompletionIdentity{Key: b.Key, SessionID: d.sessionID, UserSeq: b.UserSeq, UserMessageID: b.UserMessageID}, func() error {
			return d.store.ResolveCheckpointChanges(ctx, d.sessionID, d.assistantSeq, d.summaryID, b, d.changes)
		})
	} else {
		err = d.store.UpdateTurnFileChanges(ctx, d.sessionID, d.assistantSeq, d.summaryID, d.changes)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, checkpoint.ErrUserMessageChanged) {
			d.flushed = true
			d.changes = nil
			return
		}
		log.Printf("checkpoint deferred changes: %v", fmt.Errorf("update saved response: %w", err))
		return
	}
	d.flushed = true
	d.changes = nil
}
