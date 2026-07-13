package agent

import (
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestBuiltinSubAgents_ReturnsExpectedSet(t *testing.T) {
	agents := BuiltinSubAgents()
	// general, explore, plan, review, code + advisor (Task B second opinion).
	if len(agents) != 6 {
		t.Fatalf("BuiltinSubAgents() = %d, want 6", len(agents))
	}
}

func TestBuiltinSubAgents_HasGeneralDefault(t *testing.T) {
	// The general worker is the default when task is called without an
	// explicit agent kind, so it must exist and inherit the full tool set.
	g := findAgent(BuiltinSubAgents(), "general")
	if g == nil {
		t.Fatal("general default agent not found")
	}
	if len(g.AllowedTools) != 0 {
		t.Errorf("general should inherit all tools (nil AllowedTools), got %v", g.AllowedTools)
	}
}

func TestBuiltinSubAgents_AllHaveNames(t *testing.T) {
	for _, a := range BuiltinSubAgents() {
		if a.Name == "" {
			t.Error("sub-agent name is empty")
		}
		if a.System == "" {
			t.Errorf("%s: system prompt is empty", a.Name)
		}
		if a.Description == "" {
			t.Errorf("%s: description is empty", a.Name)
		}
	}
}

func TestBuiltinSubAgents_ExploreHasAllowedTools(t *testing.T) {
	agents := BuiltinSubAgents()
	explore := findAgent(agents, "explore")
	if explore == nil {
		t.Fatal("explore agent not found")
	}
	if len(explore.AllowedTools) < 2 {
		t.Errorf("explore should have at least 2 allowed tools, got %d", len(explore.AllowedTools))
	}
}

func TestBuiltinSubAgents_MaxStepsDefault(t *testing.T) {
	agents := BuiltinSubAgents()
	for _, a := range agents {
		if a.MaxSteps <= 0 {
			t.Errorf("%s: MaxSteps = %d, want positive", a.Name, a.MaxSteps)
		}
	}
}

func TestMustRegisterAll(t *testing.T) {
	reg := NewSubAgentRegistry()
	specs := BuiltinSubAgents()
	MustRegisterAll(reg, specs)
	for _, s := range specs {
		if _, ok := reg.Get(s.Name); !ok {
			t.Errorf("%s not found after MustRegisterAll", s.Name)
		}
	}
}

func TestSubAgentNamesAreStableForTaskSchema(t *testing.T) {
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents())
	if got, want := strings.Join(reg.Names(), ","), "advisor,code,explore,general,plan,review"; got != want {
		t.Fatalf("Names() = %q, want %q", got, want)
	}
}

func TestMustRegisterAll_EmptyList(t *testing.T) {
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, nil)
	MustRegisterAll(reg, []SubAgent{})
}

func TestPromptFromChild(t *testing.T) {
	msg := promptFromChild("hello world")
	if msg.Role != llm.RoleUser {
		t.Errorf("role = %q, want user", msg.Role)
	}
	if msg.Content != "hello world" {
		t.Errorf("content = %q, want 'hello world'", msg.Content)
	}
}

func TestPromptFromChild_Empty(t *testing.T) {
	msg := promptFromChild("")
	if msg.Content != "" {
		t.Errorf("empty prompt should produce empty content, got %q", msg.Content)
	}
}

func findAgent(agents []SubAgent, name string) *SubAgent {
	for i := range agents {
		if agents[i].Name == name {
			return &agents[i]
		}
	}
	return nil
}

// Turn-economy soft budget: the file-changing workers (general, code)
// must carry the proportionality discipline — no-op is a valid report,
// no formatting-only edits — and it must stay small (it is a constant
// cost in every worker's prefix).
func TestBuiltin_SoftBudgetInWorkerPrompts(t *testing.T) {
	agents := BuiltinSubAgents()
	for _, name := range []string{"general", "code"} {
		a := findAgent(agents, name)
		if a == nil {
			t.Fatalf("agent %q missing", name)
		}
		hasNoop := strings.Contains(a.System, "no-op") || strings.Contains(a.System, "no change is needed")
		if !hasNoop || !strings.Contains(a.System, "formatting-only") {
			t.Errorf("%s system prompt missing the soft-budget rules:\n%s", name, a.System)
		}
		if len(a.System) > 900 {
			t.Errorf("%s system prompt grew to %d chars — keep worker prefixes small", name, len(a.System))
		}
	}
}
