package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func compactionVisibilityHistory() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleUser, Content: "HIDDEN-OLD-REQUEST"},
		{Role: llm.RoleAssistant, Content: strings.Repeat("HIDDEN-OLD-EVIDENCE ", 10000)},
		{Role: llm.RoleUser, Content: "Summarize the visible investigation"},
		{Role: llm.RoleAssistant, Content: strings.Repeat("VISIBLE-VERIFIED-FINDING ", 1000)},
		{Role: llm.RoleUser, Content: "Keep the previous correction"},
		{Role: llm.RoleAssistant, Content: "HIDDEN-RECENT-ANSWER"},
		{Role: llm.RoleUser, Content: "Continue current task"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "active", Name: "read_lines", Arguments: "{\"file\":\"current.go\"}"}}},
		{Role: llm.RoleTool, ToolCallID: "active", Name: "read_lines", Content: "   1 | current evidence"},
	}
}

func TestCompactionRespectsHiddenHistoryAcrossEntryPoints(t *testing.T) {
	for _, mode := range []string{"manual", "auto", "model-switch"} {
		t.Run(mode, func(t *testing.T) {
			var summarized []llm.Message
			calls := 0
			l, err := NewLoop(LoopConfig{
				Provider: &stubProvider{name: "new"}, Registry: tools.NewRegistry(),
				System: "standing policy", InitialMessages: compactionVisibilityHistory(),
				WindowFor: func(string) int { return 4000 },
				Summarizer: func(_ context.Context, _ llm.Provider, msgs []llm.Message) (string, error) {
					calls++
					summarized = append([]llm.Message(nil), msgs...)
					return WrapCompactSummary("Goal: continue. Done: visible investigation. Pending: current task."), nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := l.HideRange(1, 3); err != nil {
				t.Fatal(err)
			}
			if err := l.HideRange(6, 7); err != nil {
				t.Fatal(err)
			}
			beforeTail := append([]llm.Message(nil), l.Messages[5:]...)
			l.contextModel = contextModelState{loaded: true, provider: "old", model: "old"}
			switch mode {
			case "manual":
				if _, err := l.CompactNow(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "auto":
				l.maybeAutoCompact(context.Background(), nil, "")
			case "model-switch":
				if !l.maybeModelHandoff(context.Background(), nil) {
					t.Fatal("handoff not triggered")
				}
			}
			if calls != 1 {
				t.Fatalf("summary calls=%d", calls)
			}
			input := RenderCompactTranscript(summarized)
			if strings.Contains(input, "HIDDEN-") {
				t.Errorf("hidden history reached summarizer (%d bytes)", len(input))
			}
			if !strings.Contains(input, "VISIBLE-VERIFIED-FINDING") || strings.Contains(input, "Continue current task") {
				t.Errorf("wrong summary boundary")
			}
			if !reflect.DeepEqual(l.Messages[len(l.Messages)-len(beforeTail):], beforeTail) {
				t.Error("retained recent messages/tool pairs changed")
			}
			if strings.Contains(RenderCompactTranscript(l.VisibleMessages()), "HIDDEN-") {
				t.Error("compaction revived a hidden recent message")
			}
			if l.HiddenCount() != 1 {
				t.Errorf("retained hidden flags=%d want=1", l.HiddenCount())
			}
			t.Logf("summarizer input: %d bytes, %d messages", len(input), len(summarized))
		})
	}
}

func TestCompactionDoesNotPayToSummarizeOnlyHiddenPrefix(t *testing.T) {
	for _, manual := range []bool{false, true} {
		l := &Loop{
			provider: &stubProvider{name: "test"}, route: RouteCoordinator,
			windowFor: func(string) int { return 500 },
			Messages: []llm.Message{
				{Role: llm.RoleSystem, Content: strings.Repeat("fixed-overhead", 1000)},
				{Role: llm.RoleUser, Content: "hidden old task"},
				{Role: llm.RoleAssistant, Content: "hidden old answer"},
				{Role: llm.RoleUser, Content: "previous instruction"},
				{Role: llm.RoleAssistant, Content: "previous answer"},
				{Role: llm.RoleUser, Content: "active task"},
			},
			summarizer: func(context.Context, llm.Provider, []llm.Message) (string, error) {
				t.Error("paid summary call for a wholly hidden prefix")
				return "unwanted summary", nil
			},
		}
		if err := l.HideRange(1, 3); err != nil {
			t.Fatal(err)
		}
		before := append([]llm.Message(nil), l.VisibleMessages()...)
		if manual {
			if _, err := l.CompactNow(context.Background()); err == nil {
				t.Error("manual compaction should report nothing to compact")
			}
		} else {
			l.maybeAutoCompact(context.Background(), nil, "")
		}
		if !reflect.DeepEqual(before, l.VisibleMessages()) {
			t.Error("unnecessary compaction changed the context")
		}
	}
}

func TestCompactionReductionDoesNotCountRetainedSystemPrompt(t *testing.T) {
	prefix := []llm.Message{
		{Role: llm.RoleSystem, Content: strings.Repeat("fixed policy", 2000)},
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Content: "hello"},
	}
	summary := WrapCompactSummary("Goal: greet. Done: hello. Pending: user.")
	if compactionReduces(prefix, summary) {
		t.Fatal("accepted a larger conversation because retained system tokens were counted as removed")
	}
	prefix[1].Content = strings.Repeat("real old history ", 2000)
	if !compactionReduces(prefix, summary) {
		t.Fatal("rejected actual useful reduction")
	}
}

func TestCompactionKeepsHiddenTailAfterSessionResume(t *testing.T) {
	ctx := context.Background()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(t.TempDir(), "test", "hidden compact")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	history := compactionVisibilityHistory()
	for _, m := range history {
		if err := writer.AppendMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	l, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "test"}, Registry: tools.NewRegistry(),
		Writer: writer, InitialMessages: history})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.HideRange(0, 2); err != nil {
		t.Fatal(err)
	}
	if err := l.HideRange(5, 6); err != nil {
		t.Fatal(err)
	}
	l.CompactPrefixWithSummary(WrapCompactSummary("visible findings"), 4)
	projected, err := store.ReadModelContext(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(RenderCompactTranscript(projected), "HIDDEN-") {
		t.Fatal("saved model context revived hidden tail")
	}
	archive, err := store.ReadMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	keptArchive := false
	for _, row := range archive {
		keptArchive = keptArchive || strings.Contains(row.Content, "HIDDEN-RECENT-ANSWER")
	}
	if !keptArchive {
		t.Fatal("full UI archive lost hidden evidence")
	}
	resumed, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "test"}, Registry: tools.NewRegistry(), InitialMessages: projected})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(RenderCompactTranscript(resumed.VisibleMessages()), "HIDDEN-") {
		t.Fatal("resumed loop restored hidden evidence")
	}
}
