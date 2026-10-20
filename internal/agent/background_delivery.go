package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"supercli/internal/llm"
)

// Only the conversation owner appends/persists history. Background producers
// merely enqueue reports, so they cannot split a tool batch or race compaction.
type backgroundMessage struct {
	uiActivity json.RawMessage
	content    string
	scoped     bool
	epoch      uint64
	writer     SessionWriter
	run        *backgroundRun
}

// Capture while the parent owns the conversation, before launching the worker.
// A worker finishing after /resume must save its report to its original session.
func (l *Loop) backgroundDelivery(ctx context.Context) func(context.Context, string) {
	epoch, writer := l.conversationEpoch.Load(), l.writer
	run := backgroundRunFrom(ctx)
	return func(noticeCtx context.Context, content string) {
		if strings.TrimSpace(content) != "" {
			run.publishReport()
			uiActivity, _ := noticeCtx.Value(workerUIActivityKey{}).(json.RawMessage)
			l.enqueueBackgroundMessage(backgroundMessage{content: content, scoped: true, epoch: epoch, writer: writer, run: run, uiActivity: uiActivity})
		}
	}
}

func (l *Loop) enqueueBackgroundMessage(message backgroundMessage) {
	l.backgroundMu.Lock()
	l.backgroundMessages = append(l.backgroundMessages, message)
	l.backgroundPending.Store(true)
	owned := l.sessionBusy.CompareAndSwap(false, true)
	l.backgroundMu.Unlock()
	if owned {
		l.releaseConversation()
	}
}

func (l *Loop) takeBackgroundMessagesLocked() []backgroundMessage {
	pending := l.backgroundMessages
	l.backgroundMessages = nil
	l.backgroundPending.Store(false)
	return pending
}

func (l *Loop) drainBackgroundMessages(ctx context.Context) {
	if !l.backgroundPending.Load() {
		return
	}
	l.backgroundMu.Lock()
	pending := l.takeBackgroundMessagesLocked()
	l.backgroundMu.Unlock()
	l.appendBackgroundMessages(ctx, pending)
}

func (l *Loop) appendBackgroundMessages(ctx context.Context, pending []backgroundMessage) {
	changed := false
	for _, message := range pending {
		msg := llm.Message{Role: llm.RoleUser, Content: message.content}
		if message.scoped {
			msg.Name = llm.WorkerNotificationName
			msg.Origin = &llm.ContextOrigin{Source: llm.SourceWorkerNotification}
		}
		transcript := msg
		if message.scoped && len(message.uiActivity) > 0 {
			transcript.Origin = &llm.ContextOrigin{Source: llm.SourceWorkerNotification, WorkerActivity: message.uiActivity}
		}
		if message.scoped && message.epoch != l.conversationEpoch.Load() {
			// The old transcript remains inspectable without contaminating the newly
			// selected conversation. Session writers are safe for concurrent use.
			if message.writer != nil {
				if err := message.writer.AppendMessage(ctx, canonicalWorkerReportMessage(transcript)); err != nil {
					l.persistNotify("background worker report could not be saved to its original session: " + err.Error())
				}
			}
			continue
		}
		l.Messages = append(l.Messages, msg)
		l.recordContextAddition(msg)
		l.liveContextDirty = true
		l.recordWorkerContextAddition(msg)
		message.run.acceptReport()
		l.persist(ctx, transcript)
		changed = true
	}
	if changed {
		l.resultReuse.reset()
		l.completedOps.reset()
		l.invalidateVisibleEstimate()
	}
}

// Releasing ownership and observing an empty inbox form one handshake. A report
// arriving during shutdown is either consumed here or acquires idle ownership;
// it cannot remain stranded until another user message. This never calls a model.
func (l *Loop) releaseConversation() {
	var ctx context.Context
	for {
		l.backgroundMu.Lock()
		if len(l.backgroundMessages) == 0 {
			l.sessionBusy.Store(false)
			l.backgroundMu.Unlock()
			return
		}
		pending := l.takeBackgroundMessagesLocked()
		l.backgroundMu.Unlock()
		if ctx == nil {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
		}
		l.appendBackgroundMessages(ctx, pending)
	}
}
