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
	// MaxSteps caps a worker's model calls. It overrides the
	// per-spec MaxSteps only when the spec leaves it unset (0).
	// Zero here falls back to the spec value or the built-in 10.
	MaxSteps int
	// MaxTokens caps a worker's total token spend (input+output,
	// summed across turns). When >0 a per-worker budget tracker
	// stops the child loop as soon as a turn pushes the running
	// total past the cap; the partial report is still returned
	// with a failed status. Zero = no token cap.
	MaxTokens int64
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
		Description: "Delegate a self-contained subtask to a fresh worker with " +
			"its own isolated context and tools. Only its final report returns " +
			"to you, so your context stays lean. Give a complete briefing in " +
			"prompt (the worker cannot see this conversation). Workers cannot " +
			"delegate further.",
		Schema: fmt.Sprintf(`{
			"type": "object",
			"properties": {
				"prompt": {"type": "string", "description": "self-contained briefing for the worker"},
				"expect": {"type": "string", "description": "optional: what the report must contain"},
				"agent":  {"type": "string", "enum": %s, "description": "optional worker kind; omit for a general worker"}
			},
			"required": ["prompt"]
		}`, string(enumJSON)),
		Fn: a.execute,
	}
}

// agentArgs is the parsed schema of the model-supplied JSON.
type agentArgs struct {
	Agent        string `json:"agent"`
	Prompt       string `json:"prompt"`
	Expect       string `json:"expect"`
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
	if ar.Prompt == "" {
		return tools.Result{Err: fmt.Errorf("task: prompt is empty")}, nil
	}
	// agent is optional: a bare prompt gets the general worker. This
	// keeps the lean call {"prompt": "..."} valid while still allowing
	// the model to pick a specialised kind when it wants one.
	if ar.Agent == "" {
		ar.Agent = defaultAgentKind
	}
	spec, ok := a.Registry.Get(ar.Agent)
	if !ok {
		return tools.Result{Err: fmt.Errorf("task: unknown agent %q (known: %s)", ar.Agent, strings.Join(a.Registry.Names(), ", "))}, nil
	}

	// Fold the optional `expect` into the worker's briefing so its
	// final report is shaped by what the coordinator asked for.
	workerPrompt := ar.Prompt
	if strings.TrimSpace(ar.Expect) != "" {
		workerPrompt += "\n\nYour final report must contain: " + strings.TrimSpace(ar.Expect)
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

	// Step budget: the spec value wins when set; otherwise the
	// tool-wide MaxSteps (from config), otherwise the built-in 10.
	maxSteps := spec.MaxSteps
	if maxSteps <= 0 {
		maxSteps = a.MaxSteps
	}
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

	// Token budget: a per-worker cap that stops the child loop as
	// soon as a turn pushes the running total past MaxTokens. Nil
	// when no cap is configured. The partial report is still
	// returned on a budget stop (runWorkerLoop keeps the text).
	var budget CreditTracker
	if a.MaxTokens > 0 {
		budget = newTokenBudget(a.MaxTokens)
	}

	// A worker inherits the parent's KV-cache-relevant loop settings
	// (thin tool protocol, stable toolset) and its sandbox root so it
	// behaves like the main session: same small-model reliability and
	// the same append-only, cache-friendly prefix. The worker builds
	// its own prefix from scratch (cold prefill is the accepted cost),
	// but per-turn it stays as cache-friendly as the coordinator.
	thin, stable, baseDir := a.childLoopSettings()

	loop, err := a.NewLoop(LoopConfig{
		Provider:        a.Provider,
		Caps:            a.Caps,
		Registry:        childReg,
		System:          system,
		MaxSteps:        maxSteps,
		InitialMessages: seed,
		ThinTools:       thin,
		StableToolset:   stable,
		BaseDir:         baseDir,
		CreditTracker:   budget,
		// Workers never route: they run straight on the coordinator
		// path with their restricted tool set. No navigator round-trip.
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
		a.startBackgroundWorker(w, workerPrompt, maxSteps)
		return tools.Result{Text: fmt.Sprintf(`<task-notification>
<task-id>%s</task-id>
<agent>%s</agent>
<status>running</status>
<summary>%s running in background</summary>
</task-notification>`, w.ID, w.Agent, w.Agent)}, nil
	}

	text, err := runWorkerLoop(childCtx, w, workerPrompt)
	if err != nil {
		return tools.Result{Text: renderWorkerNotification(w, text), Err: err}, nil
	}
	return tools.Result{Text: renderWorkerNotification(w, text)}, nil
}

// childLoopSettings returns the KV-cache-relevant loop settings a
// worker inherits from the parent loop: the thin tool protocol flag,
// the stable-toolset flag, and the sandbox root (BaseDir). When there
// is no parent (unit tests build the tool without one) it returns
// zero values, i.e. the historical worker behaviour.
func (a *AgentTool) childLoopSettings() (thin, stable bool, baseDir string) {
	if a.ParentLoop == nil {
		return false, false, ""
	}
	return a.ParentLoop.thinTools, a.ParentLoop.stableToolset, a.ParentLoop.baseDir
}

// defaultAgentKind is the worker used when task is called without an
// explicit agent. It must be registered (BuiltinSubAgents provides it).
const defaultAgentKind = "general"

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

// delegationTools are never handed to a worker: they are how the
// coordinator spawns and steers workers, so exposing them to a worker
// would allow unbounded nesting. Stripping them structurally (here,
// not just via spec omission) enforces a depth limit of 1 no matter
// what a sub-agent spec's AllowedTools says — including the inherit
// case, where a naive copy of the base set would otherwise include
// `task` itself.
var delegationTools = map[string]struct{}{
	"task":         {},
	"send_message": {},
	"task_stop":    {},
}

// restrictedRegistry returns a fresh registry containing only
// the tools whose names are in allowed. If allowed is empty,
// the full base registry is returned (a shallow copy). In both
// cases the delegation tools are stripped so a worker can never
// spawn another worker (depth limit 1).
//
// The result is a brand-new *tools.Registry so the parent and
// child have independent state and the child can never see a
// tool that is not in `allowed`.
func restrictedRegistry(base *tools.Registry, allowed []string) *tools.Registry {
	out := tools.NewRegistry()
	if len(allowed) == 0 {
		for _, name := range base.Names() {
			if _, blocked := delegationTools[name]; blocked {
				continue
			}
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
		if _, blocked := delegationTools[name]; blocked {
			continue
		}
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
