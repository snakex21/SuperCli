package agent

import (
	"testing"

	"supercli/internal/llm"
)

func TestBuiltinSubAgents_ReturnsFour(t *testing.T) {
	agents := BuiltinSubAgents()
	if len(agents) != 4 {
		t.Fatalf("BuiltinSubAgents() = %d, want 4", len(agents))
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
