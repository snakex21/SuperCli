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
	// TryAdd enforces the global active-worker cap (see
	// DefaultMaxActiveWorkers): failing fast here keeps the coordinator's
	// turn moving instead of silently queueing behind a full pool.
	w, err := workers.TryAdd(ar.Agent, ar.Prompt, loop)
	if err != nil {
		return tools.Result{Err: fmt.Errorf("task: %w", err)}, nil
	}
	if a.ParentLoop != nil {
		w.progress = func(ev WorkerProgressEvent) { a.ParentLoop.Emit(ev) }
	}
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
