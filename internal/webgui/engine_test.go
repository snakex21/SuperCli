package webgui

import (
	"context"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

// echoConfig returns a normalized echo-provider config for tests:
// no network, no API key, deterministic output.
func echoConfig() config.Config {
	c := config.Config{
		Provider: config.ProviderEcho,
		Model:    "echo-test",
		BaseURL:  "http://localhost",
	}
	_ = c.Normalize()
	return c
}

func TestNewEngine_EmptyHome(t *testing.T) {
	_, err := NewEngine(echoConfig(), "", t.TempDir())
	if err == nil {
		t.Fatal("expected error for empty home")
	}
}

func TestNewEngine_Echo(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if eng.Home() != dir {
		t.Errorf("Home = %q, want %q", eng.Home(), dir)
	}
	if eng.DataDir() != dir {
		t.Errorf("DataDir = %q, want %q", eng.DataDir(), dir)
	}
	if eng.ModelName() != "echo-test" {
		t.Errorf("ModelName = %q, want echo-test", eng.ModelName())
	}
}

func TestBuildProvider_Echo(t *testing.T) {
	p, err := buildProvider(echoConfig(), nil)
	if err != nil {
		t.Fatalf("buildProvider echo: %v", err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
}

func TestBuildProvider_CodexRejected(t *testing.T) {
	cfg := config.Config{Provider: config.ProviderCodex, Model: "gpt-5", BaseURL: "http://x"}
	_, err := buildProvider(cfg, nil)
	if err == nil {
		t.Fatal("expected codex to be rejected in web GUI")
	}
	if !strings.Contains(err.Error(), "codex") {
		t.Errorf("error should mention codex: %v", err)
	}
}

func TestBuildProvider_OpenAIDefault(t *testing.T) {
	cfg := config.Config{Provider: config.ProviderOpenAI, Model: "gpt-4o-mini", BaseURL: "https://api.openai.com/v1"}
	p, err := buildProvider(cfg, nil)
	if err != nil {
		t.Fatalf("buildProvider openai: %v", err)
	}
	if p.Name() != "gpt-4o-mini" {
		t.Errorf("Name = %q", p.Name())
	}
}

func TestEngine_NewLoop(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	loop, err := eng.newLoop()
	if err != nil {
		t.Fatalf("newLoop: %v", err)
	}
	if loop == nil {
		t.Fatal("nil loop")
	}
}

func TestEngine_RunStream_Echo(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var types []string
	var sessionID string
	err = eng.runStream(ctx, "hello world", "", func(ev wireEvent) {
		types = append(types, ev.Type)
		if ev.Type == "session" {
			sessionID = ev.SessionID
		}
	})
	if err != nil {
		t.Fatalf("runStream: %v", err)
	}
	// The echo provider always produces at least one message and a
	// terminal done event.
	if len(types) == 0 {
		t.Fatal("no events emitted")
	}
	last := types[len(types)-1]
	if last != "done" && last != "error" {
		t.Errorf("stream did not terminate cleanly: last = %q (all: %v)", last, types)
	}
	if sessionID == "" {
		t.Fatal("runStream did not emit session id")
	}
	sessions, err := eng.listSessions(ctx, 10)
	if err != nil {
		t.Fatalf("listSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != sessionID || sessions[0].MessageCount == 0 {
		t.Fatalf("persisted session mismatch: id=%q sessions=%+v", sessionID, sessions)
	}
}

func TestEngine_DataPanels_EmptyStores(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	ctx := context.Background()
	// Empty data dir: every panel must degrade gracefully, never error.
	if sessions, err := eng.listSessions(ctx, 10); err != nil || sessions == nil {
		t.Errorf("listSessions empty: %v (sessions=%v)", err, sessions)
	}
	if mem, err := eng.memoryList("", 10); err != nil || mem == nil {
		t.Errorf("memoryList empty: %v", err)
	}
	if g, err := eng.activeGoal(ctx); err != nil || g != nil {
		t.Errorf("activeGoal empty: g=%v err=%v", g, err)
	}
	sv, err := eng.stats(ctx, "")
	if err != nil {
		t.Errorf("stats empty: %v", err)
	}
	if sv.Model != "echo-test" {
		t.Errorf("stats model = %q", sv.Model)
	}
}

func TestEngine_TranscriptDecodesAssistantTextParts(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	store, err := session.OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	sess, err := store.Create(dir, "echo-test", "hello")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writer := session.NewWriter(store, sess.ID)
	if err := writer.AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: "hello"}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if err := writer.AppendMessage(context.Background(), llm.Message{
		Role:  llm.RoleAssistant,
		Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "AI answer"}},
	}); err != nil {
		t.Fatalf("append assistant: %v", err)
	}

	msgs, err := eng.transcript(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len = %d, want 2 (%+v)", len(msgs), msgs)
	}
	if msgs[1].Role != string(llm.RoleAssistant) || msgs[1].Content != "AI answer" {
		t.Fatalf("assistant transcript = %+v", msgs[1])
	}
}
