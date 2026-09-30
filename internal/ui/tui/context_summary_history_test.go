package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func TestResumeLegacySummaryKeepsModelContextWithoutFakeUserMessage(t *testing.T) {
	for _, withProjection := range []bool{false, true} {
		t.Run(map[bool]string{false: "full history fallback", true: "saved projection"}[withProjection], func(t *testing.T) {
			ctx := context.Background()
			store, err := session.OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			old, err := store.Create(t.TempDir(), "echo", "Legacy history")
			if err != nil {
				t.Fatal(err)
			}
			writer := session.NewWriter(store, old.ID)
			summary := agent.WrapCompactSummary("Goal: continue the interrupted task. Done: initial inspection.")
			msgs := []llm.Message{
				{Role: llm.RoleUser, Content: "original question"},
				{Role: llm.RoleAssistant, Content: "original answer"},
				{Role: llm.RoleUser, Content: summary},
				{Role: llm.RoleUser, Content: "continue the task"},
			}
			for _, msg := range msgs {
				if err := writer.AppendMessage(ctx, msg); err != nil {
					t.Fatal(err)
				}
			}
			if withProjection {
				if err := writer.SaveContextProjection(ctx, msgs[2:]); err != nil {
					t.Fatal(err)
				}
			}
			var resumed []llm.Message
			m := New(Options{Home: old.Cwd, SessionStore: store, NoColor: true, Language: "en", ResumeSession: func(_ context.Context, id string, model []llm.Message, _ []string) error {
				if id != old.ID {
					t.Fatalf("session changed: %q", id)
				}
				resumed = model
				return nil
			}})
			m.width, m.height = 80, 25
			next, cmd := m.resumeConversation(old.ID)
			next, _ = next.(Model).Update(cmd())
			m = next.(Model)
			rendered := ansi.Strip(m.chat.render(m.palette))
			for _, text := range []string{"original question", "original answer", "continue the task"} {
				if !strings.Contains(rendered, text) {
					t.Fatalf("real message %q missing: %s", text, rendered)
				}
			}
			for _, text := range []string{"compacted to save context", "Goal: continue", "initial inspection"} {
				if strings.Contains(rendered, text) || strings.Contains(m.transcript.String(), text) {
					t.Fatalf("internal summary leaked into chat: %s", rendered)
				}
			}
			found := false
			for _, msg := range resumed {
				found = found || msg.Content == summary
			}
			if !found {
				t.Fatalf("resume lost internal context: %+v", resumed)
			}
			if m.busy || m.sessionID != old.ID || !m.viewport.AtBottom() {
				t.Fatal("resume state not restored")
			}
		})
	}
}
