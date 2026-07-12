package webgui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/llm/factory"
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
	p, err := factory.Default(echoConfig(), "", nil)
	if err != nil {
		t.Fatalf("buildProvider echo: %v", err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
}

func TestBuildProvider_CodexRejected(t *testing.T) {
	cfg := config.Config{Provider: config.ProviderCodex, Model: "gpt-5", BaseURL: "http://x"}
	_, err := factory.Default(cfg, "", nil)
	if err == nil {
		t.Fatal("expected codex to be rejected in web GUI")
	}
	if !strings.Contains(err.Error(), "codex") {
		t.Errorf("error should mention codex: %v", err)
	}
}

func TestBuildProvider_OpenAIDefault(t *testing.T) {
	cfg := config.Config{Provider: config.ProviderOpenAI, Model: "gpt-4o-mini", BaseURL: "https://api.openai.com/v1"}
	p, err := factory.Default(cfg, "", nil)
	if err != nil {
		t.Fatalf("buildProvider openai: %v", err)
	}
	if p.Name() != "gpt-4o-mini" {
		t.Errorf("Name = %q", p.Name())
	}
}

func TestBuildProvider_Responses(t *testing.T) {
	cfg := config.Config{Provider: config.ProviderResponses, Model: "gpt-responses", BaseURL: "https://example.test/v1", APIKey: "key"}
	p, err := factory.Default(cfg, "", nil)
	if err != nil {
		t.Fatalf("buildProvider responses: %v", err)
	}
	if p.Name() != "gpt-responses" {
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

func TestEngine_WebOrchestratorRestrictsParentAndKeepsTask(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	on := true
	if err := config.SaveToml(filepath.Join(dataDir, "config.toml"), config.TomlConfig{Orchestrator: &on}); err != nil {
		t.Fatal(err)
	}
	loop, err := eng.newLoop()
	if err != nil {
		t.Fatal(err)
	}
	names := strings.Join(loop.VisibleToolNames(), "|")
	if !strings.Contains(names, "task") {
		t.Fatalf("orchestrator tools missing task: %s", names)
	}
	if strings.Contains(names, "write_file") || strings.Contains(names, "ctx_execute") {
		t.Fatalf("orchestrator parent still has mutating tools: %s", names)
	}
	msgs := loop.AllMessages()
	if len(msgs) == 0 || !strings.Contains(msgs[0].Content, "## Orchestrator mode") {
		t.Fatalf("web loop missing orchestrator prompt: %+v", msgs)
	}
}

type delegationScriptProvider struct {
	mu          sync.Mutex
	parentCalls int
	sawTask     bool
	childTools  map[string]bool
}

func (p *delegationScriptProvider) Name() string { return "delegation-script" }

func (p *delegationScriptProvider) Complete(_ context.Context, _ []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	hasTask := false
	for _, d := range defs {
		if d.Name == "task" {
			hasTask = true
			break
		}
	}
	p.mu.Lock()
	if hasTask {
		p.sawTask = true
		p.parentCalls++
	} else {
		p.childTools = make(map[string]bool, len(defs))
		for _, d := range defs {
			p.childTools[d.Name] = true
		}
	}
	parentCall := p.parentCalls
	p.mu.Unlock()

	ch := make(chan llm.Delta, 3)
	if !hasTask {
		ch <- llm.Delta{Role: llm.RoleAssistant, Content: "worker inspected the project"}
		ch <- llm.Delta{FinishReason: "stop"}
	} else if parentCall == 1 {
		ch <- llm.Delta{Role: llm.RoleAssistant, ToolCall: &llm.ToolCall{
			ID: "task-1", Name: "task", Arguments: `{"agent":"explore","prompt":"Inspect the project and report."}`,
		}}
		ch <- llm.Delta{FinishReason: "tool_calls"}
	} else {
		ch <- llm.Delta{Role: llm.RoleAssistant, Content: "delegation complete"}
		ch <- llm.Delta{FinishReason: "stop"}
	}
	close(ch)
	return ch, nil
}

func TestEngine_WebDelegationRunsWorkerAndEmitsWorkerEvent(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	on := true
	if err := config.SaveToml(filepath.Join(dataDir, "config.toml"), config.TomlConfig{Orchestrator: &on}); err != nil {
		t.Fatal(err)
	}
	provider := &delegationScriptProvider{}
	eng.mu.Lock()
	eng.prov = provider
	eng.mu.Unlock()

	var events []wireEvent
	if err := eng.runStream(context.Background(), "delegate this", "", func(ev wireEvent) {
		events = append(events, ev)
	}); err != nil {
		t.Fatal(err)
	}
	var sawTaskCall, sawWorker, sawDone bool
	for _, ev := range events {
		if ev.Type == "tool_call" && ev.Name == "task" {
			sawTaskCall = true
		}
		if ev.Type == "worker" && ev.Name == "explore" && ev.Status == "done" {
			sawWorker = true
		}
		if ev.Type == "done" {
			sawDone = true
		}
	}
	provider.mu.Lock()
	providerTask := provider.sawTask
	childSearch := provider.childTools["search_code"]
	childReadImage := provider.childTools["read_image"]
	provider.mu.Unlock()
	if !providerTask || !childSearch || !childReadImage || !sawTaskCall || !sawWorker || !sawDone {
		t.Fatalf("delegation incomplete: providerTask=%v taskCall=%v worker=%v done=%v events=%+v",
			providerTask, sawTaskCall, sawWorker, sawDone, events)
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
	if sv.Cost.Calls != 0 || sv.Cost.UnknownCalls != 0 || sv.Cost.IncludedCalls != 0 {
		t.Errorf("empty stats preview must not invent calls: %+v", sv.Cost)
	}
}

func TestEngine_ListSessionsFiltersActiveWorkspace(t *testing.T) {
	dataDir := t.TempDir()
	projectA := t.TempDir()
	projectB := t.TempDir()
	eng, err := NewEngine(echoConfig(), projectA, dataDir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	store, err := session.OpenStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	create := func(cwd, text string) string {
		sess, err := store.Create(cwd, "echo-test", text)
		if err != nil {
			t.Fatal(err)
		}
		if err := session.NewWriter(store, sess.ID).AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: text}); err != nil {
			t.Fatal(err)
		}
		return sess.ID
	}
	aID := create(projectA, "from A")
	_ = create(projectB, "from B")

	got, err := eng.listSessions(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != aID {
		t.Fatalf("project A sessions = %+v, want only %s", got, aID)
	}

	eng.setHome(projectB)
	got, err = eng.listSessions(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].FirstUserMsg != "from B" {
		t.Fatalf("project B sessions = %+v, want only B", got)
	}
}

func TestEngine_RejectsSessionFromAnotherWorkspace(t *testing.T) {
	dataDir := t.TempDir()
	projectA := t.TempDir()
	projectB := t.TempDir()
	eng, err := NewEngine(echoConfig(), projectA, dataDir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	store, err := session.OpenStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(projectB, "echo-test", "foreign")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.NewWriter(store, sess.ID).AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: "foreign"}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	if _, err := eng.transcript(context.Background(), sess.ID); !errors.Is(err, errSessionOutsideWorkspace) {
		t.Fatalf("transcript error = %v, want workspace rejection", err)
	}
	if _, _, closeStore, _, err := eng.sessionState(context.Background(), "continue", sess.ID, projectA); !errors.Is(err, errSessionOutsideWorkspace) {
		closeStore()
		t.Fatalf("sessionState error = %v, want workspace rejection", err)
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

// TestEngine_ProvidersComeOutMetered is the web GUI's factory
// contract: the engine's main provider and any task-worker override
// are llm.Metered, so web calls share the CLI's purpose ledger,
// background gate and foreground preemption.
func TestEngine_ProvidersComeOutMetered(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if !llm.IsMetered(eng.prov) {
		t.Fatalf("engine provider is not metered: %T", eng.prov)
	}
	if wp, _ := eng.taskWorkerProvider(config.TomlConfig{TaskModel: "other-echo"}); wp == nil || !llm.IsMetered(wp) {
		t.Fatalf("task worker provider is not metered: %T", wp)
	}
	if err := eng.SwitchModel("echo-two", ""); err != nil {
		t.Fatalf("SwitchModel: %v", err)
	}
	if !llm.IsMetered(eng.prov) {
		t.Fatalf("provider after SwitchModel is not metered: %T", eng.prov)
	}
}
