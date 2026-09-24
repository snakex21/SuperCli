package webgui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func capsuleCostFixture(t testing.TB, turns int) (*Engine, string) {
	t.Helper()
	eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(eng.Home(), "echo-test", "Capsule benchmark")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	toolText := strings.Repeat("source file line with project implementation details\n", 320)
	for i := 0; i < turns; i++ {
		for _, msg := range []llm.Message{
			{Role: llm.RoleUser, Content: fmt.Sprintf("Fix parser case %d and retain Unicode identifiers.", i)},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "read", Name: "read_lines", Arguments: `{"path":"parser.go"}`}}},
			{Role: llm.RoleTool, ToolCallID: "read", Content: toolText},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "test", Name: "ctx_execute", Arguments: `{"command":"go test ./..."}`}}},
			{Role: llm.RoleTool, ToolCallID: "test", Content: toolText},
			{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: fmt.Sprintf("Parser case %d fixed; Unicode identifiers preserved; tests passed.", i)}}},
		} {
			if err := writer.AppendMessage(context.Background(), msg); err != nil {
				t.Fatal(err)
			}
		}
	}
	return eng, sess.ID
}

func BenchmarkWebSessionCapsule(b *testing.B) {
	for _, turns := range []int{10, 200} {
		b.Run(fmt.Sprintf("%d_turns", turns), func(b *testing.B) {
			eng, sid := capsuleCostFixture(b, turns)
			ctx := context.Background()
			store, _ := eng.sessionStore()
			rows, err := store.ReadMessages(ctx, sid)
			if err != nil {
				b.Fatal(err)
			}
			want := buildWebSessionCapsule(sid, rows)
			eng.saveWebSessionCapsule(ctx, sid)
			_, mem := eng.webMemoryStores(eng.Home())
			initial, err := mem.Get("web-session-" + sid)
			if err != nil || initial.Content != want || want == "" {
				b.Fatalf("initial capsule: %v", err)
			}
			b.ReportAllocs()
			for b.Loop() {
				eng.saveWebSessionCapsule(ctx, sid)
			}
			got, err := mem.Get("web-session-" + sid)
			if err != nil || got.Content != want || !got.CreatedAt.Equal(initial.CreatedAt) {
				b.Fatalf("capsule changed: %v", err)
			}
		})
	}
}
