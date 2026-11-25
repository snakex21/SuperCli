package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func recallSession(t *testing.T, s *session.Store, title string, msgs ...llm.Message) session.Session {
	t.Helper()
	sess, err := s.Create("/home/test", "m", title)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		enc, err := session.FromMessage(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AppendMessage(context.Background(), sess.ID, enc); err != nil {
			t.Fatal(err)
		}
	}
	return sess
}

func runRecall(t *testing.T, tool *SessionRecall, ctx context.Context, args string) string {
	t.Helper()
	res, _ := tool.Spec().Fn(ctx, json.RawMessage(args))
	if res.Err != nil {
		return "ERR " + res.Err.Error()
	}
	return res.Text
}

// Recall finds an old user instruction and an old tool error of the current
// conversation, with turn numbers, and reads a turn in full.
func TestSessionRecallFindsOldInstructionAndToolError(t *testing.T) {
	s := openHistoryStore(t)
	var history []llm.Message
	history = append(history, llm.Message{Role: llm.RoleUser, Content: "Nigdy nie ruszaj pliku config.toml i trzymaj się stylu snake_case."})
	for i := 0; i < 40; i++ {
		history = append(history, llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("krok %d: buduję moduł mobów", i)})
	}
	history = append(history,
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "ctx_execute", Arguments: `{"command":["gradlew","build"]}`}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Name: "ctx_execute", Content: "error: command_failed exit=1\nZombieSpawner.java:42: cannot find symbol NightCycle"},
	)
	current := recallSession(t, s, "current", history...)
	tool := &SessionRecall{Store: s, Session: func(context.Context) string { return current.ID }}
	ctx := context.Background()

	got := runRecall(t, tool, ctx, `{"query":"config.toml snake_case"}`)
	if !strings.Contains(got, "turn 1 ·") || !strings.Contains(got, "config.toml") {
		t.Fatalf("old user instruction not recalled: %q", got)
	}
	got = runRecall(t, tool, ctx, `{"query":"NightCycle cannot find symbol", "role":"tool"}`)
	if !strings.Contains(got, "NightCycle") || !strings.Contains(got, "tool") {
		t.Fatalf("old tool error not recalled: %q", got)
	}
	got = runRecall(t, tool, ctx, `{"query":"moduł mobów", "from_turn":30, "to_turn":35}`)
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "turn ") && !(strings.HasPrefix(line, "turn 30 ") || strings.HasPrefix(line, "turn 31 ") || strings.HasPrefix(line, "turn 32 ") ||
			strings.HasPrefix(line, "turn 33 ") || strings.HasPrefix(line, "turn 34 ") || strings.HasPrefix(line, "turn 35 ")) {
			t.Fatalf("turn range not applied: %q", line)
		}
	}
	got = runRecall(t, tool, ctx, `{"turn":1}`)
	if !strings.Contains(got, "Nigdy nie ruszaj pliku config.toml") {
		t.Fatalf("turn read failed: %q", got)
	}
}

// Recall never reaches another conversation: not by search, not by turn.
func TestSessionRecallCannotReadOtherSessions(t *testing.T) {
	s := openHistoryStore(t)
	current := recallSession(t, s, "current", llm.Message{Role: llm.RoleUser, Content: "budujemy grę"})
	other := recallSession(t, s, "other",
		llm.Message{Role: llm.RoleUser, Content: "tajne hasło do bazy: Krakowiak2026"},
		llm.Message{Role: llm.RoleAssistant, Content: "zapisane"},
	)
	_ = other
	tool := &SessionRecall{Store: s, Session: func(context.Context) string { return current.ID }}
	ctx := context.Background()
	if got := runRecall(t, tool, ctx, `{"query":"Krakowiak2026 hasło"}`); strings.Contains(got, "Krakowiak2026") {
		t.Fatalf("another conversation leaked through search: %q", got)
	}
	if got := runRecall(t, tool, ctx, `{"turn":2}`); strings.Contains(got, "zapisane") || strings.Contains(got, "Krakowiak2026") {
		t.Fatalf("another conversation leaked through a turn read: %q", got)
	}
	// The model cannot name a session: unknown arguments are ignored.
	if got := runRecall(t, tool, ctx, fmt.Sprintf(`{"query":"Krakowiak2026","session_id":%q}`, other.ID)); strings.Contains(got, "Krakowiak2026") {
		t.Fatalf("session_id argument reached another conversation: %q", got)
	}
}

// A worker recalls from its parent conversation and its own transcript.
func TestSessionRecallForWorkers(t *testing.T) {
	s := openHistoryStore(t)
	parent := recallSession(t, s, "parent", llm.Message{Role: llm.RoleUser, Content: "Użytkownik: zombie mają spawnować się tylko w nocy."})
	own := []llm.Message{
		{Role: llm.RoleSystem, Content: "worker system"},
		{Role: llm.RoleUser, Content: "Sprawdź reguły spawnu"},
		{Role: llm.RoleTool, ToolCallID: "w1", Name: "read_lines", Content: "SpawnRules.java: lightLevel <= 7 required"},
	}
	tool := &SessionRecall{Store: s, Session: func(context.Context) string { return parent.ID },
		Worker: func(context.Context) (string, []llm.Message, bool) { return "worker-3", own, true }}
	ctx := context.Background()
	got := runRecall(t, tool, ctx, `{"query":"lightLevel nocy spawnować"}`)
	if !strings.Contains(got, "nocy") || !strings.Contains(got, "worker-3 item 3") {
		t.Fatalf("worker recall missed the parent or its own transcript: %q", got)
	}
	got = runRecall(t, tool, ctx, `{"worker_item":3}`)
	if !strings.Contains(got, "lightLevel <= 7") {
		t.Fatalf("worker item read failed: %q", got)
	}
	coordinator := &SessionRecall{Store: s, Session: func(context.Context) string { return parent.ID }}
	if got := runRecall(t, coordinator, ctx, `{"worker_item":1}`); !strings.HasPrefix(got, "ERR") {
		t.Fatalf("worker_item must be refused outside a worker: %q", got)
	}
}
