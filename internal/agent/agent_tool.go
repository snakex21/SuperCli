package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
func (a *AgentTool) execute(ctx context.Context, args json.RawMessage) (tools.Result, error) {
	var ar agentArgs
	if err := json.Unmarshal(args, &ar); err != nil {
		return tools.Result{Err: fmt.Errorf("task: bad args: %w", err)}, nil
	}
	if ar.Prompt == "" {
		return tools.Result{Err: fmt.Errorf("task: prompt is empty")}, nil
	}
	// advise (Task B) forces the read-only advisor worker regardless of any
	// agent kind the model supplied: a second opinion must never edit files.
	if ar.Advise {
		ar.Agent = advisorAgentKind
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
	// Preflight repo context (config preflight_repo): the worker's
	// context is cold, so the repo-state block saves it the initial
	// discovery turns. Rides the briefing (user message) — the
	// worker's system prefix stays stable.
	if a.Preflight != nil {
		if block := strings.TrimSpace(a.Preflight()); block != "" {
			workerPrompt += "\n\n" + block
		}
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
	thin, stable, hoist, baseDir := a.childLoopSettings()

	// Model-per-task: pick the worker backend. Defaults to the
	// coordinator's provider; a configured WorkerProvider switches the
	// child loop to a different model/host (everything else — tools,
	// thin protocol, stable toolset, sandbox, budgets — is inherited
	// identically).
	prov := a.workerProvider(ctx)

	loop, err := a.NewLoop(LoopConfig{
		Provider:        prov,
		Caps:            a.Caps,
		Registry:        childReg,
		System:          system,
		MaxSteps:        maxSteps,
		InitialMessages: seed,
		ThinTools:       thin,
		StableToolset:   stable,
		CatalogHoist:    hoist,
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
	// Telemetry: record the worker's model only when it differs from
	// the coordinator's, so the default single-model summary line is
	// byte-identical to before and draft-verify economics stay
	// measurable once a second backend is in play. Written under the
	// state lock: the worker is already published in the registry, so
	// Snapshot may be reading it concurrently.
	if prov != a.Provider {
		w.setState(func(w *Worker) { w.Model = prov.Name() })
	}
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
		a.emitWorkerNotification(w, text)
		return tools.Result{Text: renderWorkerNotification(w, text), Err: err}, nil
	}
	// Draft-verify ladder: when enabled, the completed worker run above was
	// the DRAFT. The objective sieve + big-model verdict now decide its
	// fate (accept / revise-and-retry / takeover). Disabled or unconfigured
	// = this is a no-op and the worker report returns exactly as before.
	// A read-only advisor call (Task B) never enters the ladder: it makes no
	// changes to sieve or diff, so there is nothing to verify.
	if a.draftVerifyEnabled() && !ar.Advise {
		text = a.runDraftVerify(childCtx, w, ar, workerPrompt, text, maxSteps)
	}
	a.emitWorkerNotification(w, text)
	return tools.Result{Text: renderWorkerNotification(w, text)}, nil
}

// draftVerifyEnabled reports whether the ladder is switched on. Guards every
// entry point so the OFF path stays byte-identical to pre-draft-verify code.
func (a *AgentTool) draftVerifyEnabled() bool {
	return a.DraftVerify != nil && a.DraftVerify.Enabled
}

// runDraftVerify runs the objective sieve and the big-model verdict on the
// worker's draft, looping through bounded REVISE rounds. It returns the text
// the coordinator should relay: the accepted draft, an annotated draft, or a
// TAKEOVER hand-back with the diff + evidence so the coordinator can redo it
// itself. It NEVER errors — a broken verdict falls back safely to TAKEOVER.
func (a *AgentTool) runDraftVerify(ctx context.Context, w *Worker, ar agentArgs, workerPrompt, draft string, maxSteps int) string {
	cfg := a.DraftVerify
	dir := a.sandboxRoot()
	maxRounds := cfg.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 2
	}

	tel := draftVerifyTelemetry{Outcome: "ACCEPT"}
	// Snapshot the draft's cost (the first worker run already happened).
	snap := w.Snapshot()
	tel.DraftSteps = snap.Steps
	tel.DraftTokIn = snap.TokensIn
	tel.DraftTokOut = snap.TokensOut

	best := draft
	for round := 0; ; round++ {
		tel.Rounds = round + 1
		sieve := cfg.runSieve(ctx, dir)
		if !sieve.Green {
			tel.SieveRed++
		}
		diff := cfg.gatherDiff(ctx, dir)

		v, ok, usage := cfg.requestVerdict(ctx, ar.Prompt, ar.Expect, diff, sieve)
		tel.VerifyTokIn += usage.Input
		tel.VerifyTokOut += usage.Output

		switch {
		case ok && v.Kind == verdictAccept:
			tel.Outcome = "ACCEPT"
			a.emitDraftVerify(tel)
			return best
		case ok && v.Kind == verdictRevise && round < maxRounds:
			instr := reviseInstruction(v.Instruction, sieve)
			revised, err := runWorkerLoop(ctx, w, instr)
			// Refresh the draft cost snapshot to include the revision run.
			snap = w.Snapshot()
			tel.DraftSteps = snap.Steps
			tel.DraftTokIn = snap.TokensIn
			tel.DraftTokOut = snap.TokensOut
			if err == nil && strings.TrimSpace(revised) != "" {
				best = revised
			}
			continue
		default:
			// TAKEOVER, or REVISE past the round limit, or a broken/failed
			// verdict: hand back to the coordinator with the evidence so
			// the big model can finish it. This is the safe fallback.
			outcome := "TAKEOVER"
			if ok && v.Kind == verdictRevise {
				outcome = "annotated (round limit)"
			} else if !ok {
				outcome = "TAKEOVER (unparsed verdict)"
			}
			tel.Outcome = outcome
			a.emitDraftVerify(tel)
			return draftVerifyHandback(best, diff, sieve, outcome)
		}
	}
}

// reviseInstruction composes the concrete follow-up sent back to the worker on
// a REVISE round: the big model's instruction, plus the red sieve output as
// hard evidence the worker must satisfy.
func reviseInstruction(instruction string, sieve sieveResult) string {
	var b strings.Builder
	b.WriteString("Your previous attempt was reviewed and needs revision.\n\n")
	if strings.TrimSpace(instruction) != "" {
		b.WriteString("Reviewer instruction: " + strings.TrimSpace(instruction) + "\n\n")
	}
	if !sieve.Green && !sieve.Skipped {
		fmt.Fprintf(&b, "The verify command `%s` is still failing (exit %d):\n%s\n\n",
			sieve.Command, sieve.Exit, sieve.Output)
	}
	b.WriteString("Fix the change and confirm the verify commands pass. Report only what you changed.")
	return b.String()
}

// draftVerifyHandback renders the coordinator-facing text when the ladder ends
// without a clean ACCEPT: the best draft plus the diff and sieve evidence, so
// the coordinator's big model can take over from an informed position.
func draftVerifyHandback(best, diff string, sieve sieveResult, outcome string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[draft-verify: %s — the worker draft was NOT auto-accepted]\n\n", outcome)
	b.WriteString("Worker's last report:\n" + strings.TrimSpace(best) + "\n\n")
	if !sieve.Green && !sieve.Skipped {
		fmt.Fprintf(&b, "Objective sieve RED — `%s` exited %d:\n%s\n\n", sieve.Command, sieve.Exit, sieve.Output)
	}
	if strings.TrimSpace(diff) != "" {
		b.WriteString("Diff so far:\n" + diff + "\n\n")
	}
	b.WriteString("Verify and finish this yourself using the evidence above.")
	return b.String()
}

// sandboxRoot is the directory the sieve and diff run in: the worker's inherited
// sandbox root, falling back to the process CWD when there is no parent loop.
func (a *AgentTool) sandboxRoot() string {
	if a.ParentLoop != nil && a.ParentLoop.baseDir != "" {
		return a.ParentLoop.baseDir
	}
	wd, _ := os.Getwd()
	return wd
}

// emitDraftVerify logs the economics line to the parent loop (if any) so the
// user can measure whether the ladder pays off.
func (a *AgentTool) emitDraftVerify(tel draftVerifyTelemetry) {
	if a.ParentLoop != nil {
		a.ParentLoop.Emit(NoticeEvent{Text: tel.Line()})
	}
}

// childLoopSettings returns the KV-cache-relevant loop settings a
// worker inherits from the parent loop: the thin tool protocol flag,
// the stable-toolset flag, and the sandbox root (BaseDir). When there
// is no parent (unit tests build the tool without one) it returns
// zero values, i.e. the historical worker behaviour.
func (a *AgentTool) childLoopSettings() (thin, stable, hoist bool, baseDir string) {
	if a.ParentLoop == nil {
		return false, false, false, ""
	}
	return a.ParentLoop.thinTools, a.ParentLoop.stableToolset, a.ParentLoop.catalogHoist, a.ParentLoop.baseDir
}

// workerProvider picks the LLM backend for a new worker: the
// configured WorkerProvider when it is set and (checked once, on the
// first delegation) passes WorkerPing, else the coordinator's
// Provider. An unreachable worker backend downgrades every delegation
// for the rest of the process with a single warning line — never a
// hard error, so a dead second host cannot break delegation itself.
func (a *AgentTool) workerProvider(ctx context.Context) llm.Provider {
	if a.WorkerProvider == nil {
		return a.Provider
	}
	a.workerProbe.Do(func() {
		if a.WorkerPing == nil {
			return
		}
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := a.WorkerPing(pctx); err != nil {
			a.workerDown = true
			if a.ParentLoop != nil {
				a.ParentLoop.Emit(NoticeEvent{Text: fmt.Sprintf(
					"task: worker model %q unreachable (%v) — falling back to %q",
					a.WorkerProvider.Name(), err, a.Provider.Name())})
			}
		}
	})
	if a.workerDown {
		return a.Provider
	}
	return a.WorkerProvider
}

// defaultAgentKind is the worker used when task is called without an
// explicit agent. It must be registered (BuiltinSubAgents provides it).
const defaultAgentKind = "general"

// advisorAgentKind is the read-only "second opinion" worker selected by
// task's advise:true flag (Task B). Registered by BuiltinSubAgents.
const advisorAgentKind = "advisor"

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
		}
		a.emitWorkerNotification(w, text)
	}()
}

func (a *AgentTool) emitWorkerNotification(w *Worker, text string) {
	if a.ParentLoop == nil || w == nil {
		return
	}
	a.ParentLoop.Emit(WorkerNotificationEvent{
		TaskID:  w.ID,
		Agent:   w.Agent,
		Status:  w.status(),
		Summary: workerSummary(w),
		Text:    renderWorkerNotification(w, text),
	})
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
