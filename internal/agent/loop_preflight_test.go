package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// capturingProvider records every message slice it receives so tests
// can assert on the exact request shape.
type capturingProvider struct {
	mu    sync.Mutex
	reqs  [][]llm.Message
	reply string
}

func (p *capturingProvider) Name() string { return "capture" }

func (p *capturingProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.mu.Lock()
	cp := make([]llm.Message, len(msgs))
	copy(cp, msgs)
	p.reqs = append(p.reqs, cp)
	p.mu.Unlock()
	ch := make(chan llm.Delta, 2)
	ch <- llm.Delta{Content: p.reply}
	close(ch)
	return ch, nil
}

func (p *capturingProvider) requests() [][]llm.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reqs
}

const testRepoBlock = "Repo state (auto-collected):\nbranch: main\nHEAD: abc1234 fix"

// The preflight addon must land in the FIRST user message of the
// request — the variable prompt side — and never in any system
// message (that would break the stable KV-cache prefix).
func TestPreflight_AddonRidesUserMessageNotSystem(t *testing.T) {
	prov := &capturingProvider{reply: "done"}
	l := makeLoop(t, prov, tools.NewRegistry(), "STABLE SYSTEM PROMPT")
	l.SetNextUserAddon(testRepoBlock)

	ch, err := l.Run(context.Background(), "co się ostatnio zmieniło?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	drainEvents(t, ch)

	reqs := prov.requests()
	if len(reqs) == 0 {
		t.Fatal("no provider request captured")
	}
	req := reqs[0]
	foundInUser := false
	for _, m := range req {
		has := strings.Contains(m.Content, testRepoBlock)
		switch m.Role {
		case llm.RoleUser:
			if has {
				foundInUser = true
				if !strings.Contains(m.Content, "co się ostatnio zmieniło?") {
					t.Error("addon must ride the SAME message as the user's prompt")
				}
			}
		case llm.RoleSystem:
			if has {
				t.Error("preflight block leaked into a system message")
			}
		}
	}
	if !foundInUser {
		t.Fatalf("preflight block not found in any user message:\n%+v", req)
	}
}

func TestUserAddonIsProviderOnlyAndPersistsRawPrompt(t *testing.T) {
	prov := &capturingProvider{reply: "done"}
	w := &recordingWriter{}
	l, err := NewLoop(LoopConfig{
		Provider: prov,
		Registry: tools.NewRegistry(),
		System:   "SYS",
		Writer:   w,
	})
	if err != nil {
		t.Fatal(err)
	}
	l.SetNextUserAddon("[rewind_feedback]do not repeat[/rewind_feedback]")
	ch, err := l.Run(context.Background(), "try again")
	if err != nil {
		t.Fatal(err)
	}
	drainEvents(t, ch)
	if len(w.messages) == 0 || w.messages[0].Content != "try again" {
		t.Fatalf("persisted user message = %+v, want raw prompt", w.messages)
	}
	reqs := prov.requests()
	if len(reqs) == 0 {
		t.Fatal("no provider request")
	}
	found := false
	for _, msg := range reqs[0] {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "rewind_feedback") {
			found = true
		}
	}
	if !found {
		t.Fatal("provider did not receive the one-shot addon")
	}
}

// The addon is one-shot: the second Run must not repeat it.
func TestPreflight_AddonIsOneShot(t *testing.T) {
	prov := &capturingProvider{reply: "done"}
	l := makeLoop(t, prov, tools.NewRegistry(), "SYS")
	l.SetNextUserAddon(testRepoBlock)

	ch, _ := l.Run(context.Background(), "pierwsze")
	drainEvents(t, ch)
	ch, _ = l.Run(context.Background(), "drugie")
	drainEvents(t, ch)

	reqs := prov.requests()
	if len(reqs) < 2 {
		t.Fatalf("want 2 requests, got %d", len(reqs))
	}
	last := reqs[len(reqs)-1]
	for _, m := range last {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, testRepoBlock) &&
			strings.Contains(m.Content, "drugie") {
			t.Error("addon repeated on the second Run's user message")
		}
	}
}

func TestPreflight_CoordinatorAddonSkipsChatThenRidesProjectTurn(t *testing.T) {
	prov := &capturingProvider{reply: "done"}
	l, err := NewLoop(LoopConfig{
		Provider:              prov,
		Registry:              tools.NewRegistry(),
		System:                "SYS",
		EnableNavigator:       true,
		NavigatorAuto:         true,
		NavigatorKeywordsOnly: true,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	l.SetNextCoordinatorAddon(testRepoBlock)

	ch, _ := l.Run(context.Background(), "hello")
	drainEvents(t, ch)
	reqs := prov.requests()
	if len(reqs) != 1 {
		t.Fatalf("chat requests=%d, want 1", len(reqs))
	}
	for _, msg := range reqs[0] {
		if strings.Contains(msg.Content, testRepoBlock) {
			t.Fatal("chat route paid for coordinator preflight")
		}
	}

	ch, _ = l.Run(context.Background(), "inspect project files")
	drainEvents(t, ch)
	reqs = prov.requests()
	if len(reqs) != 2 {
		t.Fatalf("total requests=%d, want 2", len(reqs))
	}
	found := false
	for _, msg := range reqs[1] {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "inspect project files") && strings.Contains(msg.Content, testRepoBlock) {
			found = true
		}
	}
	if !found {
		t.Fatal("queued preflight did not ride the next coordinator turn")
	}
}

// A delegated worker's briefing gets the preflight block appended
// when AgentTool.Preflight is set — and stays untouched when nil.
func TestPreflight_WorkerBriefingCarriesBlock(t *testing.T) {
	prov := &capturingProvider{reply: "report: ok"}
	base := tools.NewRegistry()
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents())
	at, err := NewAgentTool(reg, nil, base, prov, nil, func(cfg LoopConfig) (*Loop, error) {
		return NewLoop(cfg)
	})
	if err != nil {
		t.Fatalf("NewAgentTool: %v", err)
	}
	at.Preflight = func() string { return testRepoBlock }

	if _, err := at.execute(context.Background(), json.RawMessage(`{"prompt":"zbadaj repo"}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	reqs := prov.requests()
	if len(reqs) == 0 {
		t.Fatal("worker made no request")
	}
	found := false
	for _, m := range reqs[0] {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, "zbadaj repo") {
			found = true
			if !strings.Contains(m.Content, testRepoBlock) {
				t.Error("worker briefing missing the preflight block")
			}
		}
		if m.Role == llm.RoleSystem && strings.Contains(m.Content, testRepoBlock) {
			t.Error("preflight block leaked into the worker's system prompt")
		}
	}
	if !found {
		t.Fatal("worker briefing message not found")
	}
}
