package app

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func TestResumeLoadsLongHistoryWithoutInferenceOrTruncation(t *testing.T) {
	ctx := context.Background()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(t.TempDir(), "old", "")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	var history []llm.Message
	for i := 0; i < 12; i++ {
		history = append(history, llm.Message{Role: llm.RoleUser, Content: strings.Repeat("instruction ", 1000)}, llm.Message{Role: llm.RoleAssistant, Content: "verified result"})
	}
	for _, m := range history {
		if err := writer.AppendMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	echo, err := llm.NewEcho("new")
	if err != nil {
		t.Fatal(err)
	}
	loop, err := agent.NewLoop(agent.LoopConfig{Provider: echo, Registry: tools.NewRegistry(),
		Summarizer: func(context.Context, llm.Provider, []llm.Message) (string, error) {
			t.Fatal("resume called summarizer before new prompt")
			return "", nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = resumeSession(ctx, loop, store, func(string) int { return 1000 }, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loop.VisibleMessages(), history) {
		t.Fatal("resume rewrote or dropped original history")
	}
}

func TestResumeRestoresDiscoveredToolsFromSourceSession(t *testing.T) {
	ctx := context.Background()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(t.TempDir(), "old", "")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	if err := writer.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "prior task"}); err != nil {
		t.Fatal(err)
	}
	if err := writer.SaveDiscoveredTools(ctx, []string{"tool_search", "removed_tool"}); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.NewToolSearcher(reg, nil).Spec())
	echo, err := llm.NewEcho("new")
	if err != nil {
		t.Fatal(err)
	}
	loop, err := agent.NewLoop(agent.LoopConfig{Provider: echo, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resumeSession(ctx, loop, store, nil, sess.ID); err != nil {
		t.Fatal(err)
	}
	if !reg.IsActive("tool_search") || reg.IsActive("removed_tool") {
		t.Fatal("source session discoveries not filtered/restored")
	}
}
