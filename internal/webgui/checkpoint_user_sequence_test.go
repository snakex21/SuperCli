package webgui

import (
	"context"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"testing"
)

type checkpointNoticeProvider struct {
	inner       writingProvider
	beforeReply func() error
}

func (p *checkpointNoticeProvider) Name() string { return "synthetic-checkpoint-notice" }
func (p *checkpointNoticeProvider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	if p.inner.calls == 1 {
		if err := p.beforeReply(); err != nil {
			return nil, err
		}
	}
	return p.inner.Complete(ctx, msgs, defs)
}

// A delayed worker's user-role notice lands before the foreground terminal
// event. The checkpoint must keep the actual Run user insert, not latest(user).
func TestWebCheckpointKeepsInitiatingUserSequenceAfterNotice(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	eng, err := NewEngine(echoConfig(), home, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sid := ""
	provider := &checkpointNoticeProvider{beforeReply: func() error {
		return session.NewWriter(store, sid).AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "<task-notification>synthetic previous worker finished</task-notification>"})
	}}
	eng.mu.Lock()
	eng.prov = provider
	eng.mu.Unlock()
	if err := eng.runStream(ctx, "write the synthetic file", "", "", func(ev wireEvent) {
		if ev.Type == "session" {
			sid = ev.SessionID
		}
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := eng.checkpointManager(home)
	if err != nil {
		t.Fatal(err)
	}
	point := manager.Latest(sid)
	if point == nil || point.UserSeq != 1 {
		t.Fatalf("checkpoint followed a later notice: %+v", point)
	}
	receipt, err := store.ReadUserReceiptAt(ctx, sid, point.UserSeq)
	if err != nil || point.UserMessageID != receipt.ID || receipt.ID <= 0 {
		t.Fatalf("checkpoint lost initiating physical row: checkpoint=%+v receipt=%+v err=%v", point, receipt, err)
	}
	latest, err := store.LatestMessageSeq(ctx, sid, string(llm.RoleUser))
	if err != nil || latest <= point.UserSeq {
		t.Fatalf("fixture did not insert a later user notice: seq=%d err=%v", latest, err)
	}
	if provider.inner.calls != 2 {
		t.Fatalf("provider calls=%d, want the original two", provider.inner.calls)
	}
}
