package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func resolvedCompactFixture(t *testing.T, history []llm.Message, summary string, window int) (*Loop, *[]llm.Message, *int) {
	t.Helper()
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "search_history", Description: "search archived evidence", Schema: "{}",
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	var input []llm.Message
	calls := 0
	l, err := NewLoop(LoopConfig{
		Provider: &stubProvider{name: "fixture"}, Registry: reg, Writer: &recordingWriter{},
		System: "policy", InitialMessages: history, WindowFor: func(string) int { return window },
		Summarizer: func(_ context.Context, _ llm.Provider, messages []llm.Message) (string, error) {
			calls++
			input = append([]llm.Message(nil), messages...)
			return summary, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	l.route = RouteCoordinator
	return l, &input, &calls
}

func completedRead(id, file, content string) []llm.Message {
	return []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read_lines", Arguments: `{"file":"` + file + `"}`}}},
		{Role: llm.RoleTool, Name: "read_lines", ToolCallID: id, Content: content},
		{Role: llm.RoleAssistant, Content: "Verified."},
	}
}

func TestCompactionDoesNotCountOmittedToolEvidenceAsSavings(t *testing.T) {
	for _, mode := range []string{"manual", "auto", "context-limit", "model-switch"} {
		t.Run(mode, func(t *testing.T) {
			messages := []llm.Message{{Role: llm.RoleUser, Content: "old request"}}
			messages = append(messages, completedRead("old", "old.go", strings.Repeat("OLD-EVIDENCE ", 5000))...)
			messages = append(messages,
				llm.Message{Role: llm.RoleUser, Content: "previous correction"},
				llm.Message{Role: llm.RoleAssistant, Content: "ack"},
				llm.Message{Role: llm.RoleUser, Content: "current task"})
			summary := WrapCompactSummary(strings.Repeat("expanded summary ", 100))
			l, input, calls := resolvedCompactFixture(t, messages, summary, 5000)
			if mode != "manual" {
				// Fixed overhead triggers automatic/handoff checks without making
				// the replaceable dialogue itself larger.
				l.Messages[0].Content = strings.Repeat("fixed policy ", 15000)
			}
			before, _ := json.Marshal(l.Messages)
			oldEstimate := l.estimateNextRequestTokensRaw()
			l.contextModel = contextModelState{loaded: true, provider: "old", model: "old"}
			switch mode {
			case "manual":
				if _, err := l.CompactNow(context.Background()); err == nil || !strings.Contains(err.Error(), "insufficient reduction") {
					t.Errorf("accepted apparent savings from omitted tools: %v", err)
				}
			case "auto":
				l.maybeAutoCompact(context.Background(), nil, "")
			case "context-limit":
				l.maybeAutoCompact(context.Background(), nil, "context length exceeded")
			case "model-switch":
				if !l.maybeModelHandoff(context.Background(), nil) {
					t.Fatal("handoff did not trigger")
				}
			}
			if *calls != 1 {
				t.Errorf("summary calls=%d", *calls)
			}
			// The summarizer still needs authoritative tool evidence.
			if !strings.Contains(RenderCompactTranscript(*input), "OLD-EVIDENCE") {
				t.Error("summary lost original tool evidence")
			}
			after, _ := json.Marshal(l.Messages)
			if string(before) != string(after) {
				t.Errorf("ineffective summary replaced history: estimated request %d -> %d", oldEstimate, l.estimateNextRequestTokensRaw())
			}
			for _, message := range l.VisibleMessages() {
				if message.Content == summary {
					t.Error("larger summary reached provider context")
				}
			}
			t.Logf("estimated request: %d -> %d; summary calls: %d", oldEstimate, l.estimateNextRequestTokensRaw(), *calls)
		})
	}
}

func TestCompactionBoundaryIgnoresOmittedRecentTools(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "old investigation"},
		{Role: llm.RoleAssistant, Content: strings.Repeat("old verified finding ", 2000)},
		{Role: llm.RoleUser, Content: "PREVIOUS-CORRECTION"},
	}
	messages = append(messages, completedRead("recent", "recent.go", strings.Repeat("RECENT-EVIDENCE ", 5000))...)
	messages = append(messages,
		llm.Message{Role: llm.RoleUser, Content: "CURRENT-TASK"},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "active", Name: "read_lines", Arguments: "{}"}}},
		llm.Message{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "active", Content: "current evidence"})
	l, input, calls := resolvedCompactFixture(t, messages, WrapCompactSummary("Done: old finding. Pending: current task."), 5000)
	tail := append([]llm.Message(nil), l.Messages[3:]...)
	before := l.estimateNextRequestTokensRaw()
	if _, err := l.CompactNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	transcript := RenderCompactTranscript(*input)
	if strings.Contains(transcript, "PREVIOUS-CORRECTION") || strings.Contains(transcript, "CURRENT-TASK") {
		t.Error("omitted recent results forced full-history compaction")
	}
	if len(l.Messages) < len(tail) || !reflect.DeepEqual(l.Messages[len(l.Messages)-len(tail):], tail) {
		t.Error("recent instruction and active tool pair were replaced")
	}
	if *calls != 1 || l.estimateNextRequestTokensRaw() >= before {
		t.Error("useful compaction did not reduce provider context")
	}
}

func TestCompactionSkipsTwoShortTurnsWithArchivedToolOutput(t *testing.T) {
	messages := []llm.Message{{Role: llm.RoleUser, Content: "old request"}}
	messages = append(messages, completedRead("old", "old.go", strings.Repeat("archived result ", 5000))...)
	messages = append(messages, llm.Message{Role: llm.RoleUser, Content: "current task"})
	l, _, calls := resolvedCompactFixture(t, messages, WrapCompactSummary("Done: inspected old.go."), 50000)
	before, _ := json.Marshal(l.Messages)
	if _, err := l.CompactNow(context.Background()); err == nil || !strings.Contains(err.Error(), "nothing to compact") {
		t.Errorf("short outgoing history should not trigger a summary: %v", err)
	}
	after, _ := json.Marshal(l.Messages)
	if *calls != 0 || string(before) != string(after) {
		t.Errorf("unnecessary summary calls=%d or changed archive", *calls)
	}
}

func TestCompactionFallbackKeepsPolicyAndVisibleCorrection(t *testing.T) {
	for _, failure := range []string{"provider error", "larger summary"} {
		t.Run(failure, func(t *testing.T) {
			messages := []llm.Message{
				{Role: llm.RoleUser, Content: "old request"},
				{Role: llm.RoleAssistant, Content: strings.Repeat("old finding ", 1000)},
				{Role: llm.RoleUser, Content: "LAST-VISIBLE-CORRECTION"},
				{Role: llm.RoleAssistant, Content: "ack"},
				{Role: llm.RoleUser, Content: "HIDDEN-OBSOLETE-INSTRUCTION"},
				{Role: llm.RoleAssistant, Content: "hidden ack"},
				{Role: llm.RoleUser, Content: strings.Repeat("CURRENT-TASK ", 2000)},
			}
			l, _, _ := resolvedCompactFixture(t, messages, strings.Repeat("expanded summary ", 5000), 4000)
			if failure == "provider error" {
				l.summarizer = func(context.Context, llm.Provider, []llm.Message) (string, error) {
					return "", context.DeadlineExceeded
				}
			}
			if err := l.HideRange(5, 7); err != nil {
				t.Fatal(err)
			}
			l.maybeAutoCompact(context.Background(), nil, "")
			visible := l.VisibleMessages()
			if len(visible) == 0 || visible[0].Role != llm.RoleSystem || visible[0].Content != "policy" {
				t.Error("fallback hid standing instructions")
			}
			text := RenderCompactTranscript(visible)
			if !strings.Contains(text, "LAST-VISIBLE-CORRECTION") || !strings.Contains(text, "CURRENT-TASK") {
				t.Error("fallback discarded a protected visible turn")
			}
			if strings.Contains(text, "HIDDEN-OBSOLETE-INSTRUCTION") || strings.Contains(text, "old finding") {
				t.Error("fallback failed to omit obsolete evidence")
			}
		})
	}
}

func TestHideLastUserTurnsZeroPreservesSystem(t *testing.T) {
	l := &Loop{Messages: []llm.Message{
		{Role: llm.RoleSystem, Content: "policy"},
		{Role: llm.RoleUser, Content: "task"},
		{Role: llm.RoleAssistant, Content: "answer"},
	}}
	if hidden := l.HideLastUserTurns(0); hidden != 2 {
		t.Fatalf("hidden=%d want=2", hidden)
	}
	if visible := l.VisibleMessages(); len(visible) != 2 || visible[0].Content != "policy" {
		t.Fatal("clear removed policy")
	}
}

func TestCompactionStillPricesRequiredToolHistory(t *testing.T) {
	for _, mode := range []string{"healthy archive", "worker without writer", "no history tool", "outage", "pending", "lost", "native continuation"} {
		t.Run(mode, func(t *testing.T) {
			messages := []llm.Message{{Role: llm.RoleUser, Content: "old task"}}
			messages = append(messages, completedRead("old", "old.go", strings.Repeat("authoritative result ", 5000))...)
			messages = append(messages,
				llm.Message{Role: llm.RoleUser, Content: "previous"},
				llm.Message{Role: llm.RoleAssistant, Content: "ack"},
				llm.Message{Role: llm.RoleUser, Content: "current"})
			l, input, calls := resolvedCompactFixture(t, messages, WrapCompactSummary("Done: inspected old.go. Pending: current."), 50000)
			switch mode {
			case "worker without writer":
				l.writer = nil
			case "no history tool":
				l.registry = tools.NewRegistry()
			case "outage":
				l.persistHealth.outage = true
			case "pending":
				l.persistHealth.pending = []pendingAppend{{Message: l.Messages[3], Writer: l.writer}}
			case "lost":
				l.persistHealth.dropped = 1
			case "native continuation":
				l.Messages[2].Parts = policyReply("required state").Parts
			}
			before := l.estimateNextRequestTokensRaw()
			_, err := l.CompactNow(context.Background())
			if mode == "healthy archive" {
				if err == nil || !strings.Contains(err.Error(), "insufficient reduction") {
					t.Fatal("counted tool evidence already omitted from the healthy archive view")
				}
			} else if err != nil || l.estimateNextRequestTokensRaw() >= before {
				t.Fatalf("required tool evidence was excluded from useful compaction: %v", err)
			}
			if *calls != 1 || !strings.Contains(RenderCompactTranscript(*input), "authoritative result") {
				t.Fatal("summarizer lost evidence or made extra calls")
			}
		})
	}
}

func TestCompactionMapsMixedToolBatchAndHiddenHistory(t *testing.T) {
	ctx := context.Background()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(t.TempDir(), "fixture", "compact tool map")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "hidden obsolete task"},
		{Role: llm.RoleAssistant, Content: "hidden obsolete answer"},
	}
	mixed := mixedEvidenceHistory()
	mixed[4].Content = strings.Repeat("verified old findings ", 2000)
	messages = append(messages, mixed...)
	messages = append(messages,
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "live", Name: "read_lines", Arguments: "{}"}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "live", Name: "read_lines", Content: "LIVE-EVIDENCE"})
	l, input, _ := resolvedCompactFixture(t, messages, WrapCompactSummary("Done: inspected files. Pending: current request."), 50000)
	for _, message := range l.Messages {
		if err := writer.AppendMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	l.writer = writer
	if err := l.HideRange(1, 3); err != nil {
		t.Fatal(err)
	}
	tail := append([]llm.Message(nil), l.Messages[8:]...)
	if _, err := l.CompactNow(ctx); err != nil {
		t.Fatal(err)
	}
	if len(l.Messages) != len(tail)+2 || !reflect.DeepEqual(l.Messages[2:], tail) {
		t.Fatal("mapped boundary changed the current turn")
	}
	inputText := RenderCompactTranscript(*input)
	if !strings.Contains(inputText, "build detail") || !strings.Contains(inputText, "RetryDelay=235") ||
		strings.Contains(inputText, "LIVE-EVIDENCE") || strings.Contains(inputText, "hidden obsolete") {
		t.Fatal("wrong evidence sent for summarization")
	}
	assertEvidencePairs(t, l.providerMessages())
	saved, err := store.ReadModelContext(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != len(l.Messages)-1 || !reflect.DeepEqual(saved, l.Messages[1:]) {
		t.Fatal("saved model context differs after mapped compaction")
	}
	archive, err := store.ReadMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	originalLargeResult := false
	for _, message := range archive {
		originalLargeResult = originalLargeResult || message.Content == mixed[2].Content
	}
	if !originalLargeResult {
		t.Fatal("UI/search archive lost the full result")
	}
}

func TestCompactionPricesSmallOldToolsAgainstTheWholeConversation(t *testing.T) {
	messages := []llm.Message{{Role: llm.RoleUser, Content: "old task"}}
	messages = append(messages, completedRead("old", "old.go", strings.Repeat("small result ", 100))...)
	messages = append(messages,
		llm.Message{Role: llm.RoleUser, Content: "previous correction"},
		llm.Message{Role: llm.RoleAssistant, Content: "recent final reply"},
		llm.Message{Role: llm.RoleUser, Content: "current task"})
	summary := WrapCompactSummary("Done: inspected old.go.")
	l, _, calls := resolvedCompactFixture(t, messages, summary, 50000)
	if _, err := l.CompactNow(context.Background()); err == nil || !strings.Contains(err.Error(), "insufficient reduction") {
		t.Fatalf("an isolated prefix incorrectly kept small tools from an older completed turn: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("summary calls=%d", *calls)
	}
}
