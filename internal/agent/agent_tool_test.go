package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// --- test helpers ---

// stubReplyProvider is a minimal Provider that returns its
// scripted content. It is good enough for "child loop finishes
// with text X" tests.
type stubReplyProvider struct {
	name  string
	reply string
	delay time.Duration
}

func (e *stubReplyProvider) Name() string         { return e.name }
func (e *stubReplyProvider) SupportsVision() bool { return true }
func (e *stubReplyProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta, 4)
	go func() {
		defer close(ch)
		if e.delay > 0 {
			select {
			case <-time.After(e.delay):
			case <-ctx.Done():
				ch <- llm.Delta{Err: ctx.Err()}
				return
			}
		}
		ch <- llm.Delta{Content: e.reply}
		ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 5, Output: 3, Total: 8}}
	}()
	return ch, nil
}

func newTestBaseRegistry() *tools.Registry {
	r := tools.NewRegistry()
	r.MustRegister(tools.NewReadImage(".", 0).Spec())
	r.MustRegister(tools.NewSearchCode(".").Spec())
	r.MustRegister(tools.Tool{
		Name:        "echo",
		Description: "echoes the input back",
		Schema:      `{"type":"object","properties":{"msg":{"type":"string"}}}`,
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "echoed"}, nil
		},
	})
	return r
}

// childLoopFactory builds a child Loop that echoes whatever
// the script says, ignoring the messages. The factory is
// used by AgentTool tests.
func childLoopFactory(reply string, calls *atomic.Int32) LoopFactory {
	return func(cfg LoopConfig) (*Loop, error) {
		if calls != nil {
			calls.Add(1)
		}
		return NewLoop(LoopConfig{
			Provider:        &stubReplyProvider{name: "test", reply: reply},
			Registry:        cfg.Registry,
			Caps:            cfg.Caps,
			System:          cfg.System,
			MaxSteps:        cfg.MaxSteps,
			InitialMessages: cfg.InitialMessages,
		})
	}
}

// --- tests ---

func TestAgentTool_Spec(t *testing.T) {
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{Name: "explore", Description: "search"})
	at, err := NewAgentTool(reg, nil, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, childLoopFactory("", nil))
	if err != nil {
		t.Fatalf("NewAgentTool: %v", err)
	}
	spec := at.Spec()
	if spec.Name != "task" {
		t.Errorf("Name = %q", spec.Name)
	}
	if !strings.Contains(spec.Schema, `"explore"`) {
		t.Errorf("schema missing explore: %q", spec.Schema)
	}
	if !strings.Contains(spec.Schema, `"prompt"`) {
		t.Errorf("schema missing prompt: %q", spec.Schema)
	}
	if !strings.Contains(spec.Schema, `"share_context"`) {
		t.Errorf("schema missing share_context: %q", spec.Schema)
	}
}

func TestAgentTool_Spec_NoAgents(t *testing.T) {
	// Even with no registered sub-agents the spec must build.
	reg := NewSubAgentRegistry()
	at, _ := NewAgentTool(reg, nil, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, childLoopFactory("", nil))
	spec := at.Spec()
	if !strings.Contains(spec.Schema, `"enum"`) {
		t.Errorf("schema missing enum: %q", spec.Schema)
	}
}

func TestNewAgentTool_RejectsNilArgs(t *testing.T) {
	r := NewSubAgentRegistry()
	base := newTestBaseRegistry()
	prov := &stubReplyProvider{name: "t"}
	f := childLoopFactory("", nil)
	cases := []struct {
		name string
		fn   func() (*AgentTool, error)
	}{
		{"nil reg", func() (*AgentTool, error) { return NewAgentTool(nil, nil, base, prov, nil, f) }},
		{"nil base", func() (*AgentTool, error) { return NewAgentTool(r, nil, nil, prov, nil, f) }},
		{"nil prov", func() (*AgentTool, error) { return NewAgentTool(r, nil, base, nil, nil, f) }},
		{"nil factory", func() (*AgentTool, error) { return NewAgentTool(r, nil, base, prov, nil, nil) }},
	}
	for _, c := range cases {
		_, err := c.fn()
		if err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
}

func TestAgentTool_Execute_PassesArgs(t *testing.T) {
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{Name: "explore", Description: "search"})
	calls := atomic.Int32{}
	at, _ := NewAgentTool(reg, nil, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, childLoopFactory("found it", &calls))
	res, err := at.execute(context.Background(), json.RawMessage(`{"agent":"explore","prompt":"find X"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("res.Err: %v", res.Err)
	}
	if !strings.Contains(res.Text, "<task-id>worker-1</task-id>") || !strings.Contains(res.Text, "<result>found it</result>") {
		t.Errorf("Text = %q", res.Text)
	}
	if calls.Load() != 1 {
		t.Errorf("factory calls = %d, want 1", calls.Load())
	}
}

func TestAgentTool_Execute_RestrictsTools(t *testing.T) {
	// explore allows only search_code and read_image.
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{
		Name: "explore", Description: "search",
		AllowedTools: []string{"search_code"},
	})
	var captured *tools.Registry
	var mu sync.Mutex
	factory := func(cfg LoopConfig) (*Loop, error) {
		mu.Lock()
		captured = cfg.Registry
		mu.Unlock()
		return childLoopFactory("ok", nil)(cfg)
	}
	at, _ := NewAgentTool(reg, nil, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, factory)
	res, _ := at.execute(context.Background(), json.RawMessage(`{"agent":"explore","prompt":"x"}`))
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	mu.Lock()
	defer mu.Unlock()
	if captured == nil {
		t.Fatal("factory not called")
	}
	names := captured.Names()
	if len(names) != 1 || names[0] != "search_code" {
		t.Errorf("child registry = %v, want only search_code", names)
	}
}

func TestAgentTool_Execute_InheritsAllTools(t *testing.T) {
	reg := NewSubAgentRegistry()
	// code sub-agent: empty AllowedTools -> inherit all
	reg.MustRegister(SubAgent{Name: "code", Description: "impl"})
	var captured *tools.Registry
	factory := func(cfg LoopConfig) (*Loop, error) {
		captured = cfg.Registry
		return childLoopFactory("ok", nil)(cfg)
	}
	at, _ := NewAgentTool(reg, nil, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, factory)
	res, _ := at.execute(context.Background(), json.RawMessage(`{"agent":"code","prompt":"x"}`))
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if captured == nil {
		t.Fatal("factory not called")
	}
	if captured.Len() != newTestBaseRegistry().Len() {
		t.Errorf("len = %d, want %d", captured.Len(), newTestBaseRegistry().Len())
	}
}

func TestAgentTool_Execute_UnknownAgent(t *testing.T) {
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{Name: "explore", Description: "search"})
	at, _ := NewAgentTool(reg, nil, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, childLoopFactory("", nil))
	res, _ := at.execute(context.Background(), json.RawMessage(`{"agent":"unknown","prompt":"x"}`))
	if res.Err == nil {
		t.Fatal("expected error for unknown agent")
	}
	if !strings.Contains(res.Err.Error(), "unknown") {
		t.Errorf("Err = %v", res.Err)
	}
}

func TestAgentTool_Execute_BadArgs(t *testing.T) {
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{Name: "explore", Description: "search"})
	at, _ := NewAgentTool(reg, nil, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, childLoopFactory("", nil))
	cases := []string{
		`not json`,
		`{"agent":""}`,
		`{"agent":"explore"}`,
	}
	for _, in := range cases {
		res, _ := at.execute(context.Background(), json.RawMessage(in))
		if res.Err == nil {
			t.Errorf("input %q: expected error", in)
		}
	}
}

func TestAgentTool_Execute_FactoryError(t *testing.T) {
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{Name: "explore", Description: "search"})
	factory := func(cfg LoopConfig) (*Loop, error) {
		return nil, errors.New("boom")
	}
	at, _ := NewAgentTool(reg, nil, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, factory)
	res, _ := at.execute(context.Background(), json.RawMessage(`{"agent":"explore","prompt":"x"}`))
	if res.Err == nil {
		t.Fatal("expected error from factory")
	}
}

func TestAgentTool_Concurrent(t *testing.T) {
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{Name: "explore", Description: "search"})
	var calls atomic.Int32
	at, _ := NewAgentTool(reg, nil, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, childLoopFactory("done", &calls))

	const n = 3
	var wg sync.WaitGroup
	results := make([]string, n)
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, _ := at.execute(context.Background(), json.RawMessage(`{"agent":"explore","prompt":"p"}`))
			results[i] = res.Text
		}()
	}
	wg.Wait()
	if calls.Load() != int32(n) {
		t.Errorf("factory calls = %d, want %d", calls.Load(), n)
	}
	for i, r := range results {
		if !strings.Contains(r, "<task-id>worker-") || !strings.Contains(r, "<result>done</result>") {
			t.Errorf("results[%d] = %q", i, r)
		}
	}
}

type userCountingProvider struct{ name string }

func (p *userCountingProvider) Name() string         { return p.name }
func (p *userCountingProvider) SupportsVision() bool { return true }
func (p *userCountingProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	users := 0
	for _, m := range msgs {
		if m.Role == llm.RoleUser {
			users++
		}
	}
	ch := make(chan llm.Delta, 2)
	go func() {
		defer close(ch)
		ch <- llm.Delta{Content: fmt.Sprintf("users=%d", users)}
		ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 1, Output: 1, Total: 2}}
	}()
	return ch, nil
}

func TestAgentTool_SendMessageContinuesWorkerContext(t *testing.T) {
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{Name: "explore", Description: "search"})
	provider := &userCountingProvider{name: "counter"}
	var factoryCalls atomic.Int32
	factory := func(cfg LoopConfig) (*Loop, error) {
		factoryCalls.Add(1)
		return NewLoop(LoopConfig{
			Provider:        provider,
			Registry:        cfg.Registry,
			System:          cfg.System,
			MaxSteps:        cfg.MaxSteps,
			InitialMessages: cfg.InitialMessages,
		})
	}
	at, _ := NewAgentTool(reg, nil, newTestBaseRegistry(), provider, nil, factory)
	first, _ := at.execute(context.Background(), json.RawMessage(`{"agent":"explore","prompt":"first"}`))
	if !strings.Contains(first.Text, "<task-id>worker-1</task-id>") || !strings.Contains(first.Text, "users=1") {
		t.Fatalf("first result = %q", first.Text)
	}
	send := NewSendMessageTool(at.Workers)
	second, _ := send.execute(context.Background(), json.RawMessage(`{"to":"worker-1","message":"second"}`))
	if second.Err != nil {
		t.Fatalf("send_message err: %v", second.Err)
	}
	if !strings.Contains(second.Text, "<task-id>worker-1</task-id>") || !strings.Contains(second.Text, "users=2") {
		t.Fatalf("second result = %q", second.Text)
	}
	if factoryCalls.Load() != 1 {
		t.Fatalf("factory calls = %d, want 1", factoryCalls.Load())
	}
}

func TestAgentTool_AsyncInjectsNotificationIntoParent(t *testing.T) {
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{Name: "explore", Description: "search"})
	provider := &stubReplyProvider{name: "child", reply: "async done", delay: 20 * time.Millisecond}
	base := newTestBaseRegistry()
	parent, err := NewLoop(LoopConfig{Provider: provider, Registry: base, System: "parent"})
	if err != nil {
		t.Fatalf("parent loop: %v", err)
	}
	ext := make(chan Event, 4)
	parent.SetExternalSink(ext)
	factory := func(cfg LoopConfig) (*Loop, error) {
		return NewLoop(LoopConfig{
			Provider:        provider,
			Registry:        cfg.Registry,
			System:          cfg.System,
			MaxSteps:        cfg.MaxSteps,
			InitialMessages: cfg.InitialMessages,
		})
	}
	at, _ := NewAgentTool(reg, parent, base, provider, nil, factory)
	at.TimeoutPerStep = 200 * time.Millisecond
	res, _ := at.execute(context.Background(), json.RawMessage(`{"agent":"explore","prompt":"background","async":true}`))
	if res.Err != nil {
		t.Fatalf("task async err: %v", res.Err)
	}
	if !strings.Contains(res.Text, "<status>running</status>") || !strings.Contains(res.Text, "worker-1") {
		t.Fatalf("initial async response = %q", res.Text)
	}

	select {
	case ev := <-ext:
		n, ok := ev.(WorkerNotificationEvent)
		if !ok {
			t.Fatalf("event = %T, want WorkerNotificationEvent", ev)
		}
		if n.TaskID != "worker-1" || n.Status != "done" || !strings.Contains(n.Text, "async done") {
			t.Fatalf("notification = %+v", n)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for worker notification")
	}

	msgs := parent.AllMessages()
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleUser || !strings.Contains(last.Content, "<task-id>worker-1</task-id>") || !strings.Contains(last.Content, "async done") {
		t.Fatalf("last parent message = %+v", last)
	}
}

func TestAgentTool_Timeout_Respected(t *testing.T) {
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{Name: "slow", Description: "slow", MaxSteps: 1})
	// Child loop takes 200ms; the timeout is 50ms.
	slowFactory := func(cfg LoopConfig) (*Loop, error) {
		return NewLoop(LoopConfig{
			Provider: &stubReplyProvider{name: "slow", reply: "hi", delay: 200 * time.Millisecond},
			Registry: cfg.Registry,
			MaxSteps: 1,
		})
	}
	at, _ := NewAgentTool(reg, nil, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, slowFactory)
	at.TimeoutPerStep = 50 * time.Millisecond
	res, _ := at.execute(context.Background(), json.RawMessage(`{"agent":"slow","prompt":"x"}`))
	if res.Err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestAgentTool_ShareContext_True(t *testing.T) {
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{Name: "explore", Description: "search"})
	var seenInitial int
	factory := func(cfg LoopConfig) (*Loop, error) {
		seenInitial = len(cfg.InitialMessages)
		return childLoopFactory("ok", nil)(cfg)
	}
	parent := &Loop{Messages: []llm.Message{
		{Role: llm.RoleSystem, Content: "parent system"},
		{Role: llm.RoleUser, Content: "earlier user prompt"},
		{Role: llm.RoleAssistant, Content: "earlier assistant reply"},
	}}
	at, _ := NewAgentTool(reg, parent, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, factory)
	res, _ := at.execute(context.Background(), json.RawMessage(`{"agent":"explore","prompt":"x","share_context":true}`))
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	// system is excluded; 2 user/assistant messages survive.
	if seenInitial != 2 {
		t.Errorf("seed len = %d, want 2", seenInitial)
	}
}

func TestAgentTool_ShareContext_DefaultFalse(t *testing.T) {
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{Name: "explore", Description: "search"})
	var seenInitial int
	factory := func(cfg LoopConfig) (*Loop, error) {
		seenInitial = len(cfg.InitialMessages)
		return childLoopFactory("ok", nil)(cfg)
	}
	parent := &Loop{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "earlier"},
		{Role: llm.RoleAssistant, Content: "reply"},
	}}
	at, _ := NewAgentTool(reg, parent, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, factory)
	res, _ := at.execute(context.Background(), json.RawMessage(`{"agent":"explore","prompt":"x"}`))
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if seenInitial != 0 {
		t.Errorf("seed len = %d, want 0", seenInitial)
	}
}

func TestAgentTool_Execute_AppliesSystemOverride(t *testing.T) {
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{
		Name: "explore", Description: "search",
		System: "narrow voice",
	})
	var seenSystem string
	factory := func(cfg LoopConfig) (*Loop, error) {
		seenSystem = cfg.System
		return childLoopFactory("ok", nil)(cfg)
	}
	parent := &Loop{system: "parent voice", Messages: []llm.Message{}}
	at, _ := NewAgentTool(reg, parent, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, factory)
	res, _ := at.execute(context.Background(), json.RawMessage(`{"agent":"explore","prompt":"x"}`))
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if seenSystem != "narrow voice" {
		t.Errorf("system = %q, want narrow voice", seenSystem)
	}
}

func TestAgentTool_Execute_InheritsParentSystem(t *testing.T) {
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{Name: "code", Description: "impl"}) // no system
	var seenSystem string
	factory := func(cfg LoopConfig) (*Loop, error) {
		seenSystem = cfg.System
		return childLoopFactory("ok", nil)(cfg)
	}
	parent := &Loop{system: "parent voice", Messages: []llm.Message{}}
	at, _ := NewAgentTool(reg, parent, newTestBaseRegistry(), &stubReplyProvider{name: "t"}, nil, factory)
	res, _ := at.execute(context.Background(), json.RawMessage(`{"agent":"code","prompt":"x"}`))
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if seenSystem != "parent voice" {
		t.Errorf("system = %q, want parent voice", seenSystem)
	}
}

func TestRestrictedRegistry_Filtered(t *testing.T) {
	base := newTestBaseRegistry()
	got := restrictedRegistry(base, []string{"search_code"})
	if got.Len() != 1 || got.Names()[0] != "search_code" {
		t.Errorf("got = %v", got.Names())
	}
}

func TestRestrictedRegistry_Inherits(t *testing.T) {
	base := newTestBaseRegistry()
	got := restrictedRegistry(base, nil)
	if got.Len() != base.Len() {
		t.Errorf("got len %d, want %d", got.Len(), base.Len())
	}
}

func TestRestrictedRegistry_UnknownTool(t *testing.T) {
	base := newTestBaseRegistry()
	got := restrictedRegistry(base, []string{"search_code", "nope"})
	if got.Len() != 1 {
		t.Errorf("got = %v, want 1 (unknown silently skipped)", got.Names())
	}
}

func TestAllowedTools_Dedup(t *testing.T) {
	got := allowedTools("a", "b", "a", "", "c", "b")
	if len(got) != 3 || got[0] != "a" {
		t.Errorf("got = %v", got)
	}
}

func TestBuiltinSubAgents_RegisterCleanly(t *testing.T) {
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents())
	names := reg.Names()
	want := map[string]bool{"explore": true, "plan": true, "review": true, "code": true}
	if len(names) != 4 {
		t.Errorf("len = %d, want 4", len(names))
	}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected built-in: %q", n)
		}
	}
}
