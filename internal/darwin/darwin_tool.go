package darwin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// DarwinTool is the tools.Tool the agent can call
// to invoke Darwin mode. It owns its own
// Darwin orchestrator and a configurable
// LoopFactory (set by main.go with
// darwin.AgentLoopAdapter()).
type DarwinTool struct {
	provider  llm.Provider
	registry  *tools.Registry
	home      string
	system    string
	loopFact  LoopFactory
}

// NewDarwinTool returns a DarwinTool ready to
// register in the tool registry.
func NewDarwinTool(provider llm.Provider, registry *tools.Registry, home, system string) *DarwinTool {
	return &DarwinTool{
		provider: provider,
		registry: registry,
		home:     home,
		system:   system,
	}
}

// SetLoopFactory configures the loop factory.
// Must be called before Spec().Fn is invoked.
// Typically set to darwin.AgentLoopAdapter() in
// main.go.
func (d *DarwinTool) SetLoopFactory(f LoopFactory) {
	d.loopFact = f
}

// Spec returns the tool spec for registration in
// the tools.Registry.
func (d *DarwinTool) Spec() tools.Tool {
	return tools.Tool{
		Name:        "darwin",
		Description: "Spawn N independent agents in parallel (optionally in isolated git worktrees), pick the best result with a judge, and optionally auto-merge the winner into the caller's branch. Use for hard problems where diverse attempts improve quality.",
		Schema:      darwinSchema,
		Fn:          d.run,
	}
}

// darwinArgs is the JSON payload the model sends.
type darwinArgs struct {
	Prompt     string `json:"prompt"`
	PoolSize   int    `json:"pool_size,omitempty"`
	NoWorktree bool   `json:"no_worktree,omitempty"`
	AutoMerge  bool   `json:"auto_merge,omitempty"`
	Judge      string `json:"judge,omitempty"` // "composite" | "llm" | "heuristic"
}

// darwinSchema is the JSON Schema string the tool
// advertises. Kept as a string constant to avoid
// pulling in a JSON Schema library.
const darwinSchema = `{
  "type": "object",
  "required": ["prompt"],
  "properties": {
    "prompt":      {"type": "string", "description": "The task to give each agent."},
    "pool_size":   {"type": "integer", "minimum": 1, "maximum": 10, "description": "How many agents to spawn in parallel. Default 3."},
    "no_worktree": {"type": "boolean", "description": "If true, agents work in the caller's cwd instead of isolated git worktrees."},
    "auto_merge":  {"type": "boolean", "description": "If true, merge the winning branch into the caller's current branch. Refused for main/master/develop."},
    "judge":       {"type": "string", "enum": ["composite", "llm", "heuristic"], "description": "Judge strategy. Default 'composite' (LLM with heuristic fallback)."}
  }
}`

// run is the tool entry point. It blocks until
// the Darwin run finishes (the model is single-
// threaded per turn, so this is fine).
func (d *DarwinTool) run(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
	if d == nil {
		return tools.Result{}, fmt.Errorf("darwin: nil tool")
	}
	if d.loopFact == nil {
		return tools.Result{}, fmt.Errorf("darwin: tool not initialised (call SetLoopFactory)")
	}
	var args darwinArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return tools.Result{}, fmt.Errorf("darwin: parse args: %w", err)
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return tools.Result{}, fmt.Errorf("darwin: prompt is required")
	}
	if args.PoolSize < 0 || args.PoolSize > 10 {
		return tools.Result{}, fmt.Errorf("darwin: pool_size must be in [1, 10]")
	}
	var judge Judge
	switch args.Judge {
	case "", "composite":
		judge = &CompositeJudge{
			Primary:  &LLMJudge{Provider: d.provider},
			Fallback: NewHeuristicJudge(),
		}
	case "llm":
		judge = &LLMJudge{Provider: d.provider}
	case "heuristic":
		judge = NewHeuristicJudge()
	default:
		return tools.Result{}, fmt.Errorf("darwin: unknown judge %q (want composite|llm|heuristic)", args.Judge)
	}
	dw, err := NewDarwin(Config{
		PoolConfig: PoolConfig{
			Provider:  d.provider,
			System:    d.system,
			Home:      d.home,
			Factory:   d.loopFact,
			PoolSize:  args.PoolSize,
		},
		Judge:       judge,
		AutoMerge:   args.AutoMerge,
		UseWorktree: !args.NoWorktree,
		OnEvent:     nil, // could be wired to a hook in future
	})
	if err != nil {
		return tools.Result{}, err
	}
	stream, err := dw.Run(ctx, args.Prompt)
	if err != nil {
		return tools.Result{}, err
	}
	// Drain the channel; we care only about the
	// final Result.
	var final *Result
	for ev := range stream {
		if de, ok := ev.(DoneEvent); ok {
			r := de.Result
			final = &r
		}
		if ee, ok := ev.(ErrorEvent); ok {
			return tools.Result{}, ee.Err
		}
	}
	if final == nil {
		return tools.Result{}, fmt.Errorf("darwin: run closed without DoneEvent")
	}
	return tools.Result{Text: renderDarwinResult(*final)}, nil
}

// renderDarwinResult formats a Result as a
// markdown block the model can quote in its final
// answer.
func renderDarwinResult(r Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Darwin run result\n\n")
	fmt.Fprintf(&b, "- **prompt:** %s\n", r.Prompt)
	fmt.Fprintf(&b, "- **candidates:** %d\n", len(r.Candidates))
	if r.Winner != nil {
		fmt.Fprintf(&b, "- **winner:** %s (score=%.2f)\n", r.Winner.AgentID, r.Score)
		fmt.Fprintf(&b, "- **reason:** %s\n", r.Reason)
	} else {
		fmt.Fprintf(&b, "- **winner:** <none>\n")
	}
	if r.Merged {
		fmt.Fprintf(&b, "- merged: yes (from `%s`)\n", r.MergeBranch)
	} else {
		fmt.Fprintf(&b, "- merged: no\n")
	}
	fmt.Fprintf(&b, "- **total tokens:** %d\n\n", r.TotalUsage.Total)
	if len(r.Candidates) > 0 {
		fmt.Fprintf(&b, "### Candidates\n\n")
		for i, c := range r.Candidates {
			marker := "  "
			if i == r.WinnerIndex {
				marker = "★ "
			}
			if c.Err != nil {
				fmt.Fprintf(&b, "%scandidate %d (%s): [FAILED: %v]\n", marker, i+1, c.AgentID, c.Err)
			} else {
				fmt.Fprintf(&b, "%scandidate %d (%s): %s\n", marker, i+1, c.AgentID, truncate(c.Text, 600))
			}
		}
	}
	if r.Winner != nil && r.Winner.Err == nil {
		fmt.Fprintf(&b, "\n### Winning answer\n\n%s\n", r.Winner.Text)
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
