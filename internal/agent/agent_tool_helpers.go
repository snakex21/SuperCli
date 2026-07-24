package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

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
			// read_output closes over its registry-owned OutputStore. Copying
			// the Tool value would make the child read from the parent's store
			// while compacting into its own. NewLoop installs a fresh closure
			// for the child registry instead.
			if name == "read_output" {
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
		if name == "read_output" {
			continue // NewLoop installs the child registry's own store closure.
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
