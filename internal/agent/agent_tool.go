package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
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
	// WorkerProvider, when non-nil, is the LLM backend delegated
	// workers run on instead of Provider (config `task_model`). The
	// coordinator keeps using Provider; only child loops switch. Nil =
	// workers inherit Provider (default, byte-identical behaviour).
	WorkerProvider llm.Provider
	// WorkerPing verifies the worker backend, lazily, on the first
	// delegation only (never on the startup path). Nil = no probe. On
	// failure every delegation permanently falls back to Provider and
	// a single warning NoticeEvent is emitted on the parent loop.
	WorkerPing func(context.Context) error

	// Preflight, when non-nil, returns a compact repo-state block
	// (config preflight_repo) appended to every worker's briefing.
	// A worker starts with a cold, isolated context, so this is where
	// the turn saving is largest: it skips the "where am I" discovery
	// turns. Nil = no block (default, byte-identical behaviour).
	Preflight func() string

	// DraftVerify configures the draft-verify ladder (config
	// `draft_verify`). Nil or disabled = task delegation is byte-identical
	// to before: the worker report returns as-is with no sieve and no
	// verdict. When enabled and a synchronous worker touched files, the
	// objective sieve runs and the coordinator's model issues a verdict on
	// the diff + evidence (see draftverify.go).
	DraftVerify *DraftVerifyConfig

	// workerProbe gates the one-time WorkerPing; workerDown records a
	// failed probe so later delegations skip straight to the fallback.
	workerProbe sync.Once
	workerDown  bool
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
				"agent":  {"type": "string", "enum": %s, "description": "optional worker kind; omit for a general worker"},
				"advise": {"type": "boolean", "description": "optional: ask a READ-ONLY second opinion on a decision (routed to the advisor worker; never edits files)"}
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
	// Advise routes the call to the read-only advisor worker (Task B,
	// "second opinion"): a one-off question answered by another model
	// (task_model) with no file edits and no draft-verify ladder.
	Advise bool `json:"advise"`
}

// execute is the Fn wired into Spec. It parses args, builds a
// restricted child registry, runs the child loop, and returns
// the final assistant text. Errors include both protocol
// problems (unknown agent) and child-loop failures.
