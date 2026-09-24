package agent

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func TestResumeRejectsActiveRunAndUnpersistedHistory(t *testing.T) {
	store, _ := session.OpenStore(t.TempDir())
	defer store.Close()
	old, _ := store.Create(t.TempDir(), "echo", "old")
	next, _ := store.Create(old.Cwd, "echo", "next")
	provider, _ := llm.NewEcho("echo")
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: tools.NewRegistry(), Writer: session.NewWriter(store, old.ID)})
	if err != nil {
		t.Fatal(err)
	}
	history := []llm.Message{{Role: llm.RoleUser, Content: "restored"}}
	loop.sessionBusy.Store(true)
	if err := loop.ResumeConversation(context.Background(), session.NewWriter(store, next.ID), history, nil); err == nil {
		t.Fatal("switched while running")
	}
	loop.sessionBusy.Store(false)
	loop.persistHealth.pending = []llm.Message{{Role: llm.RoleAssistant, Content: "not yet saved"}}
	if err := loop.ResumeConversation(context.Background(), session.NewWriter(store, next.ID), history, nil); err == nil {
		t.Fatal("lost pending writes")
	}
	if loop.SessionID() != old.ID {
		t.Fatal("writer changed after rejected switch")
	}
	loop.persistHealth.pending = nil
	loaded := append([]llm.Message{{Role: llm.RoleSystem, Content: "obsolete prompt"}}, history...)
	if err := loop.ResumeConversation(context.Background(), session.NewWriter(store, next.ID), loaded, nil); err != nil {
		t.Fatal(err)
	}
	for _, m := range loop.Messages {
		if strings.Contains(m.Content, "obsolete prompt") {
			t.Fatal("duplicated old system prompt")
		}
	}
	if loop.SessionID() != next.ID {
		t.Fatal("writer did not switch")
	}
}
