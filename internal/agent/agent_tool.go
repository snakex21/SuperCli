package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// LoopFactory creates a fresh Loop. The AgentTool uses it to
// spin up child loops with a restricted tool registry. Tests
// pass a factory that returns a stub Loop so the dispatcher
// can be exercised without a real LLM.
type LoopFactory func(cfg LoopConfig) (*Loop, error)

// AgentTool exposes the SubAgentRegistry to the model as a
// single tool named "task". The model calls it with
// {"agent": "explore", "prompt": "..."} and the AgentTool
// spawns a child Loop in a goroutine, waits for it to finish,
// and returns the final assistant text.
type AgentTool struct {
	Registry     *SubAgentRegistry
	ParentLoop   *Loop // optional; used for context sharing and parent system prompt
	BaseRegistry *tools.Registry
	Provider     llm.Provider // passed to every child loop
	Caps         *llm.CapabilityRegistry
	NewLoop      LoopFactory
	Workers      *WorkerRegistry
	// TimeoutPerStep is the budget for one model step in the
	// child loop. The total child timeout is MaxSteps * this.
	// Zero means 30s per step (matches the design's "5 min for
	// MaxSteps=10" default).
	TimeoutPerStep time.Duration
}

// NewAgentTool returns the tool. reg, base, factory, and
// provider are required; caps and parent are optional.
func NewAgentTool(reg *SubAgentRegistry, parent *Loop, base *tools.Registry, provider llm.Provider, caps *llm.CapabilityRegistry, factory LoopFactory) (*AgentTool, error) {
	if reg == nil {
		return nil, fmt.Errorf("agent.NewAgentTool: registry is nil")
	}
	if base == nil {
		return nil, fmt.Errorf("agent.NewAgentTool: base registry is nil")
	}
	if provider == nil {
		return nil, fmt.Errorf("agent.NewAgentTool: provider is nil")
	}
	if factory == nil {
		return nil, fmt.Errorf("agent.NewAgentTool: NewLoop factory is nil")
	}
	return &AgentTool{
		Registry:       reg,
		ParentLoop:     parent,
		BaseRegistry:   base,
		Provider:       provider,
		Caps:           caps,
		NewLoop:        factory,
		Workers:        NewWorkerRegistry(),
		TimeoutPerStep: 30 * time.Second,
	}, nil
}

// Spec returns the tools.Tool description. The schema lists
// the registered sub-agents in the `enum` of the `agent`
// parameter so the model cannot pick a name that does not
// exist.
func (a *AgentTool) Spec() tools.Tool {
	names := a.Registry.Names()
	enumJSON, _ := json.Marshal(names)
	return tools.Tool{
		Name: "task",
		Description: "Spawn a sub-agent to handle a focused subtask. " +
			"Use this when the parent loop would benefit from delegating " +
			"(e.g. exploration, planning, code review). The sub-agent has " +
			"its own system prompt, restricted tool set, and isolated " +
			"context. Returns a task-notification with a worker id; use " +
			"send_message to continue the same worker when its context helps.",
		Schema: fmt.Sprintf(`{
			"type": "object",
			"properties": {
				"agent":         {"type": "string", "enum": %s, "description": "which sub-agent kind to spawn"},
				"prompt":        {"type": "string", "description": "task for the sub-agent"},
				"async":         {"type": "boolean", "default": false, "description": "if true, run in background and return immediately; completion is delivered as a task-notification"},
				"share_context": {"type": "boolean", "default": false, "description": "include parent messages as initial context"}
			},
			"required": ["agent", "prompt"]
		}`, string(enumJSON)),
		Fn: a.execute,
	}
}

// agentArgs is the parsed schema of the model-supplied JSON.
type agentArgs struct {
	Agent        string `json:"agent"`
	Prompt       string `json:"prompt"`
	Async        bool   `json:"async"`
	ShareContext bool   `json:"share_context"`
}

// execute is the Fn wired into Spec. It parses args, builds a
// restricted child registry, runs the child loop, and returns
// the final assistant text. Errors include both protocol
// problems (unknown agent) and child-loop failures.
func (a *AgentTool) execute(ctx context.Context, args json.RawMessage) (tools.Result, error) {
	var ar agentArgs
	if err := json.Unmarshal(args, &ar); err != nil {
		return tools.Result{Err: fmt.Errorf("task: bad args: %w", err)}, nil
	}
	if ar.Agent == "" {
		return tools.Result{Err: fmt.Errorf("task: agent is empty")}, nil
	}
	if ar.Prompt == "" {
		return tools.Result{Err: fmt.Errorf("task: prompt is empty")}, nil
	}
	spec, ok := a.Registry.Get(ar.Agent)
	if !ok {
		return tools.Result{Err: fmt.Errorf("task: unknown agent %q (known: %s)", ar.Agent, strings.Join(a.Registry.Names(), ", "))}, nil
	}

	// Build the child's tool registry: only the tools the
	// spec allows, or the full set when the spec inherits.
	childReg := restrictedRegistry(a.BaseRegistry, spec.AllowedTools)

	// Decide on the seed messages. When share_context is
	// true and we have a parent loop, copy its messages
	// (minus the system prompt, which we re-derive).
	seed := []llm.Message(nil)
	if ar.ShareContext && a.ParentLoop != nil {
		for _, m := range a.ParentLoop.Messages {
			if m.Role == llm.RoleSystem {
				continue
			}
			seed = append(seed, m)
		}
	}

	// Compose the system prompt: parent system + spec
	// override. Spec wins when set, allowing the sub-agent
	// to "narrow" the parent's voice.
	system := spec.System
	if system == "" && a.ParentLoop != nil {
		system = a.ParentLoop.system
	}

	maxSteps := spec.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 10
	}

	// Apply the child timeout. The parent context may also
	// be cancelled, in which case we just propagate.
	childCtx := ctx
	if a.TimeoutPerStep > 0 {
		timeout := a.TimeoutPerStep * time.Duration(maxSteps)
		var cancel context.CancelFunc
		childCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	loop, err := a.NewLoop(LoopConfig{
		Provider:        a.Provider,
		Caps:            a.Caps,
		Registry:        childReg,
		System:          system,
		MaxSteps:        maxSteps,
		InitialMessages: seed,
	})
	if err != nil {
		return tools.Result{Err: fmt.Errorf("task: child loop: %w", err)}, nil
	}
	workers := a.Workers
	if workers == nil {
		workers = NewWorkerRegistry()
		a.Workers = workers
	}
	w := workers.Add(ar.Agent, ar.Prompt, loop)
	if ar.Async {
		a.startBackgroundWorker(w, ar.Prompt, maxSteps)
		return tools.Result{Text: fmt.Sprintf(`<task-notification>
<task-id>%s</task-id>
<agent>%s</agent>
<status>running</status>
<summary>%s running in background</summary>
</task-notification>`, w.ID, w.Agent, w.Agent)}, nil
	}

	text, err := runWorkerLoop(childCtx, w, ar.Prompt)
	if err != nil {
		return tools.Result{Text: renderWorkerNotification(w, text), Err: err}, nil
	}
	return tools.Result{Text: renderWorkerNotification(w, text)}, nil
}

func (a *AgentTool) startBackgroundWorker(w *Worker, prompt string, maxSteps int) {
	if w == nil {
		return
	}
	timeout := a.TimeoutPerStep * time.Duration(maxSteps)
	if timeout <= 0 {
		timeout = 30 * time.Second * time.Duration(maxSteps)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		text, err := runWorkerLoop(ctx, w, prompt)
		if err != nil && text == "" {
			text = err.Error()
		}
		notification := renderWorkerNotification(w, text)
		if a.ParentLoop != nil {
			a.ParentLoop.InjectUserMessage(context.Background(), notification)
			a.ParentLoop.Emit(WorkerNotificationEvent{
				TaskID:  w.ID,
				Agent:   w.Agent,
				Status:  w.Status,
				Summary: workerSummary(w),
				Text:    notification,
			})
		}
	}()
}

// restrictedRegistry returns a fresh registry containing only
// the tools whose names are in allowed. If allowed is empty,
// the full base registry is returned (a shallow copy).
//
// The result is a brand-new *tools.Registry so the parent and
// child have independent state and the child can never see a
// tool that is not in `allowed`.
func restrictedRegistry(base *tools.Registry, allowed []string) *tools.Registry {
	out := tools.NewRegistry()
	if len(allowed) == 0 {
		for _, name := range base.Names() {
			t, ok := base.Get(name)
			if !ok {
				continue
			}
			_ = out.Register(t)
			out.MarkAlwaysOn(t.Name)
		}
		return out
	}
	for _, name := range allowed {
		t, ok := base.Get(name)
		if !ok {
			// unknown tool in spec — skip silently; the
			// spec author will see the empty registry at
			// integration time.
			continue
		}
		_ = out.Register(t)
		out.MarkAlwaysOn(t.Name)
	}
	return out
}

// allowedTools is a tiny helper used by the built-in
// sub-agents list (see builtin.go). It de-duplicates while
// preserving order.
func allowedTools(list ...string) []string {
	seen := make(map[string]struct{}, len(list))
	out := make([]string, 0, len(list))
	for _, n := range list {
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}
