package agent

import (
	"context"
	"fmt"
	"sync"
)

// SubAgent is the static spec for a sub-agent kind. F3 will
// turn it into a fully wired Loop (its own system prompt,
// allowed tools, model). For F2 we only need the registry and
// the ability to enumerate kinds.
type SubAgent struct {
	Name         string
	Description  string
	System       string
	AllowedTools []string // empty = inherit
	Model        string   // empty = inherit
	MaxSteps     int      // 0 = inherit
}

// Validate returns nil if the spec is well-formed.
func (s SubAgent) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("agent.SubAgent: name is empty")
	}
	if s.Description == "" {
		return fmt.Errorf("agent.SubAgent(%s): description is empty", s.Name)
	}
	if s.MaxSteps < 0 {
		return fmt.Errorf("agent.SubAgent(%s): max steps is negative", s.Name)
	}
	return nil
}

// SubAgentRegistry holds the named sub-agents known to the
// parent agent. It is safe for concurrent use.
type SubAgentRegistry struct {
	mu     sync.RWMutex
	agents map[string]SubAgent
}

// NewSubAgentRegistry returns an empty registry.
func NewSubAgentRegistry() *SubAgentRegistry {
	return &SubAgentRegistry{agents: make(map[string]SubAgent)}
}

// Register adds a sub-agent. Empty name or duplicate name is
// rejected.
func (r *SubAgentRegistry) Register(a SubAgent) error {
	if err := a.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.agents[a.Name]; exists {
		return fmt.Errorf("agent.SubAgentRegistry: duplicate %q", a.Name)
	}
	r.agents[a.Name] = a
	return nil
}

// MustRegister panics on error; use only in init code.
func (r *SubAgentRegistry) MustRegister(a SubAgent) {
	if err := r.Register(a); err != nil {
		panic(err)
	}
}

// Get returns the sub-agent by name and a found flag.
func (r *SubAgentRegistry) Get(name string) (SubAgent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[name]
	return a, ok
}

// Names returns the registered sub-agent names in unspecified
// order. Used to build tool schemas and to render the available
// agents list.
func (r *SubAgentRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.agents))
	for n := range r.agents {
		out = append(out, n)
	}
	return out
}

// Len reports how many sub-agents are registered.
func (r *SubAgentRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.agents)
}

// SubAgentRunner is the contract F3 will provide: a function
// that, given a SubAgent spec and a prompt, returns the final
// assistant text. The factory is injectable so tests can use
// a deterministic mock.
//
// F2 only defines the type; the actual implementation is in
// F3 once the AgentTool is in place.
type SubAgentRunner func(ctx context.Context, spec SubAgent, prompt string) (string, error)

// Run executes a named sub-agent via the provided runner. The
// runner is responsible for creating a child Loop, building a
// restricted registry, and emitting the final text. This
// function is a thin wrapper used by the F3 AgentTool.
func (r *SubAgentRegistry) Run(ctx context.Context, runner SubAgentRunner, name, prompt string) (string, error) {
	spec, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("agent.SubAgentRegistry.Run: unknown sub-agent %q", name)
	}
	if runner == nil {
		return "", fmt.Errorf("agent.SubAgentRegistry.Run: runner is nil")
	}
	return runner(ctx, spec, prompt)
}
