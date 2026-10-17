package darwin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// DarwinTool is the tools.Tool the agent can call
// to invoke Darwin mode. It owns its own
// Darwin orchestrator and a configurable
// LoopFactory (set by main.go with
// darwin.AgentLoopAdapter()).
type DarwinTool struct {
	provider         llm.Provider
	providerResolver func() llm.Provider
	mu               sync.RWMutex
	registry         *tools.Registry
	home             string
	system           string
	loopFact         LoopFactory
	sequential       bool
	models           []llm.Provider
	judgeProvider    llm.Provider
	allowAutoMerge   bool
	resultSink       func(Result) (string, error)
	reasoningEffort  string
	poolSize         int // config darwin_pool_size; 0 = model decides
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

// SetReasoningEffort scopes both candidate runs and the judge; empty inherits global.
func (d *DarwinTool) SetReasoningEffort(effort string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reasoningEffort = effort
}

// SetPoolSize fixes the candidate count (config darwin_pool_size). 0 lets
// the model choose via pool_size (default 3); 1..10 overrides the model's
// value. Out-of-range values are clamped to [1, 10].
func (d *DarwinTool) SetPoolSize(n int) {
	if n < 0 {
		n = 0
	} else if n > maxPoolSize {
		n = maxPoolSize
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.poolSize = n
}

// SetLoopFactory configures the loop factory.
// Must be called before Spec().Fn is invoked.
// Typically set to darwin.AgentLoopAdapter() in
// main.go.
func (d *DarwinTool) SetLoopFactory(f LoopFactory) {
	d.loopFact = f
}

// SetSequential forces the pool to run agents one at a time (see
// PoolConfig.Sequential). main.go sets this for local backends, where
// N concurrent contexts serialize on one GPU slot and thrash each
// other's KV cache anyway. When sequential, the rendered result carries
// a "~N× time" note so the user understands the wall-clock cost.
func (d *DarwinTool) SetSequential(seq bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sequential = seq
}

// SetModels assigns candidate models round-robin (config darwin_models).
// Nil or empty = every candidate uses the active model.
func (d *DarwinTool) SetModels(ps []llm.Provider) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.models = ps
}

// SetJudgeProvider sets the judge model (config darwin_judge_model).
// Nil = the active model judges.
func (d *DarwinTool) SetJudgeProvider(p llm.Provider) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.judgeProvider = p
}

// SetAllowAutoMerge permits the auto_merge argument (config
// darwin_auto_merge). Off by default: the model alone cannot merge
// into the user's branch; the winner stays on its own branch.
func (d *DarwinTool) SetAllowAutoMerge(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.allowAutoMerge = on
}

// SetResultSink hands each finished run to a UI (the GUI's Darwin card),
// which returns a run id. With a sink, every changed candidate keeps its
// branch so the user can apply any of them, and the result text carries
// a `darwin_run_id:` marker. Nil = TUI behaviour (winner branch only).
func (d *DarwinTool) SetResultSink(f func(Result) (string, error)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.resultSink = f
}

// Sequential reports whether the tool runs agents sequentially.
func (d *DarwinTool) Sequential() bool { d.mu.RLock(); defer d.mu.RUnlock(); return d.sequential }

// SetProviderResolver selects the active model at invocation time. Configure before use.
func (d *DarwinTool) SetProviderResolver(f func() llm.Provider) { d.providerResolver = f }

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
    "pool_size":   {"type": "integer", "minimum": 1, "maximum": 10, "description": "How many agents to spawn in parallel. Default 3. Ignored when the user fixed darwin_pool_size in config."},
    "no_worktree": {"type": "boolean", "description": "If true, agents work in the caller's cwd instead of isolated git worktrees."},
    "auto_merge":  {"type": "boolean", "description": "If true, merge the winning branch into the caller's current branch. Ignored unless the user enabled darwin_auto_merge; refused for main/master/develop."},
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
	d.mu.RLock()
	fixedPool := d.poolSize
	d.mu.RUnlock()
	if fixedPool > 0 {
		// The user fixed darwin_pool_size: the model's value is ignored.
		args.PoolSize = fixedPool
	} else if args.PoolSize < 0 || args.PoolSize > 10 {
		return tools.Result{}, fmt.Errorf("darwin: pool_size must be in [1, 10]")
	}
	provider := d.provider
	if d.providerResolver != nil {
		provider = d.providerResolver()
	}
	sequential := d.Sequential()
	d.mu.RLock()
	models := append([]llm.Provider(nil), d.models...)
	judgeProvider := d.judgeProvider
	allowMerge := d.allowAutoMerge
	sink := d.resultSink
	effort := d.reasoningEffort
	d.mu.RUnlock()
	if judgeProvider == nil {
		judgeProvider = provider
	}
	autoMerge := args.AutoMerge && allowMerge
	var judge Judge
	switch args.Judge {
	case "", "composite":
		judge = &CompositeJudge{
			Primary:  &LLMJudge{Provider: judgeProvider},
			Fallback: NewHeuristicJudge(),
		}
	case "llm":
		judge = &LLMJudge{Provider: judgeProvider}
	case "heuristic":
		judge = NewHeuristicJudge()
	default:
		return tools.Result{}, fmt.Errorf("darwin: unknown judge %q (want composite|llm|heuristic)", args.Judge)
	}
	dw, err := NewDarwin(Config{
		PoolConfig: PoolConfig{
			Provider:   provider,
			Providers:  models,
			System:     d.system,
			Home:       d.home,
			Factory:    d.loopFact,
			PoolSize:   args.PoolSize,
			Sequential: sequential,
		},
		Judge:       judge,
		AutoMerge:   autoMerge,
		UseWorktree: !args.NoWorktree,
		KeepAll:     sink != nil,
		OnEvent:     nil, // could be wired to a hook in future
	})
	if err != nil {
		return tools.Result{}, err
	}
	ctx = llm.WithIndependentReasoningEffort(ctx, effort)
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
	text := renderDarwinResult(*final)
	if sink != nil {
		if id, sErr := sink(*final); sErr == nil && id != "" {
			text += "\n_The user reviews every candidate in the GUI Darwin card and applies or discards them there; do not merge or delete darwin branches yourself._\n\ndarwin_run_id: " + id + "\n"
		} else {
			// No card to clean up from: drop the extra loser branches.
			var extra []string
			for i, c := range final.Candidates {
				if c.Kept && i != final.WinnerIndex {
					extra = append(extra, c.Branch)
				}
			}
			_ = DeleteBranches(context.Background(), d.home, extra)
		}
	}
	if args.AutoMerge && !allowMerge {
		text = "_auto_merge ignored: darwin_auto_merge is off, the user applies the winner manually_\n\n" + text
	}
	if sequential {
		n := len(final.Candidates)
		text = fmt.Sprintf("_local backend: sequential best-of-%d, expect ~%d× time_\n\n%s", n, n, text)
	}
	return tools.Result{Text: text}, nil
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
	if r.KeptBranch != "" {
		fmt.Fprintf(&b, "- **winner branch:** `%s` (kept)\n", r.KeptBranch)
		if !r.Merged {
			fmt.Fprintf(&b, "  - review: `git diff HEAD...%s`\n", r.KeptBranch)
			fmt.Fprintf(&b, "  - apply: `git merge --no-ff %s`\n", r.KeptBranch)
		}
		fmt.Fprintf(&b, "  - discard: `git branch -D %s`\n", r.KeptBranch)
	} else if r.Winner != nil && r.Winner.Err == nil && r.Winner.Branch != "" && strings.TrimSpace(r.Winner.Diff) == "" {
		fmt.Fprintf(&b, "- **winner branch:** none kept (winner changed no files)\n")
	}
	fmt.Fprintf(&b, "- **total tokens:** %d\n", r.TotalUsage.Total)
	if r.Note != "" {
		fmt.Fprintf(&b, "- **note:** %s\n", r.Note)
	}
	b.WriteString("\n")
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
				changed := ""
				if c.Branch != "" {
					changed = " [no file changes]"
					if s := lastLine(c.DiffStat); s != "" {
						changed = " [" + strings.TrimSpace(s) + "]"
					}
				}
				model := ""
				if c.Model != "" {
					model = ", " + c.Model
				}
				fmt.Fprintf(&b, "%scandidate %d (%s%s)%s: %s\n", marker, i+1, c.AgentID, model, changed, truncate(c.Text, 600))
			}
		}
	}
	if r.Winner != nil && r.Winner.Err == nil {
		fmt.Fprintf(&b, "\n### Winning answer\n\n%s\n", r.Winner.Text)
		if r.Winner.DiffStat != "" {
			fmt.Fprintf(&b, "\n### Winning diffstat\n\n```\n%s\n```\n", r.Winner.DiffStat)
		}
		if r.Winner.Diff != "" {
			fmt.Fprintf(&b, "\n### Winning diff\n\n```diff\n%s\n```\n", truncate(r.Winner.Diff, 4000))
		}
	}
	return b.String()
}

// lastLine returns the last non-empty line of s
// (the "N files changed, ..." summary of --stat).
func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\r\n"), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
