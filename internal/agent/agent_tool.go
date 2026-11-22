package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/execution"
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
	Provider     llm.Provider // explicit/default child backend; same-parent construction inherits live swaps
	Caps         *llm.CapabilityRegistry
	NewLoop      LoopFactory
	Workers      *WorkerRegistry
	// TimeoutPerStep is the budget for one model step in the
	// child loop. The total child timeout is MaxSteps * this.
	// Zero means the constructor's 30s-per-step default.
	TimeoutPerStep time.Duration
	// MaxSteps caps a worker's model calls. It overrides the
	// per-spec MaxSteps only when the spec leaves it unset (0).
	// Zero here falls back to the spec value or DefaultMaxSteps.
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
	// WorkerReasoningEffort is scoped to delegated requests, even on a shared provider.
	WorkerReasoningEffort string
	// AdvisorReasoningEffort is independent of the delegated worker preference.
	AdvisorReasoningEffort string
	// WorkerProfile is resolved for WorkerProvider, not the coordinator. Only
	// its tool protocol/cache fields apply; nil preserves inherited settings.
	WorkerProfile *execution.Profile
	// WorkerContextProvider scopes learned context and prefill profiles to the
	// actual configured connection, independent of whether it is local HTTP or
	// remote HTTP. Empty inherits the parent's connection identity.
	WorkerContextProvider string
	// WorkerProviderName / CoordinatorProviderName name the backends the
	// worker's calls go to (task_model or the coordinator's), for the
	// per-provider concurrency cap max_parallel_agents_per_provider.
	WorkerProviderName      string
	CoordinatorProviderName string
	// AdvisorProvider, when non-nil, runs advise:true calls (config
	// `advisor_model`), e.g. a cloud model advising a local one. Nil = the
	// advisor uses the normal worker backend.
	AdvisorProvider llm.Provider
	// AdvisorOff (config advisor_model = "off") removes the advise flag
	// from the schema and refuses advise:true calls: the user turned the
	// second opinion off.
	AdvisorOff      bool
	PrefillProfiles *llm.PrefillProfiles
	// WorkerPing verifies the worker backend, lazily, on the first
	// delegation only (never on the startup path). Nil = no probe. On
	// backend failure every delegation falls back to Provider and a single
	// warning NoticeEvent is emitted. Caller cancellation is not a failed probe.
	WorkerPing func(context.Context) error

	// Preflight, when non-nil, returns a compact repo-state block
	// (config preflight_repo) appended to every worker's briefing.
	// A worker starts with a cold, isolated context, so this is where
	// the turn saving is largest: it skips the "where am I" discovery
	// turns. Nil = no block (default, byte-identical behaviour).
	Preflight func() string
	// PreflightContext is the cancelable alternative used by application
	// entry points. When set, it takes precedence over legacy Preflight.
	PreflightContext func(context.Context) string

	// DraftVerify configures the draft-verify ladder (config
	// `draft_verify`). Nil or disabled = task delegation is byte-identical
	// to before: the worker report returns as-is with no sieve and no
	// verdict. When enabled and a synchronous worker touched files, the
	// objective sieve runs and the coordinator's model issues a verdict on
	// the diff + evidence (see draftverify.go).
	DraftVerify *DraftVerifyConfig

	// Only the constructor can opt into parent-provider inheritance. A distinct
	// injected Provider and legacy struct literals retain their explicit backend.
	inheritParentProvider bool

	// A completed probe is cached; caller cancellation leaves it untested.
	// Concurrent delegations share one probe and can cancel their own wait.
	// VerifyCommands (config verify_commands) run after a file-changing
	// worker says done; a red one marks the worker "needs review".
	VerifyCommands []string
	// verifyRunner replaces the command runner in tests.
	verifyRunner func(ctx context.Context, dir, command string, timeout time.Duration) (int, string)

	// startMu makes the duplicate-task check and the worker registration
	// one step (parallel identical task calls in one response).
	startMu sync.Mutex

	workerProbeMu   sync.Mutex
	workerProbeDone chan struct{}
	workerProbed    bool
	workerDown      bool
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
	a := &AgentTool{
		Registry:              reg,
		ParentLoop:            parent,
		BaseRegistry:          base,
		Provider:              provider,
		Caps:                  caps,
		NewLoop:               factory,
		Workers:               NewWorkerRegistry(),
		TimeoutPerStep:        30 * time.Second,
		inheritParentProvider: parent != nil && sameProviderInstance(provider, parent.provider),
	}
	if parent != nil {
		// Status turns list this conversation's workers. Read a.Workers at
		// call time: hosts replace it with their shared registry after this.
		parent.workerStatusSource = a.conversationWorkersWithSaved
		parent.workerStatusLookup = a.savedWorkerStatus
	}
	return a, nil
}

// workerBriefLister is an optional WorkerStore extension that lists saved
// workers without loading their transcripts.
type workerBriefLister interface {
	ListWorkerBriefs(ctx context.Context, sessionID string) ([]WorkerRecord, error)
}

// repeatedBrief refuses a task that repeats a worker of this conversation:
// always when that worker is still running or queued, and in an automatic
// turn (no user message) whatever its state - a failed, stopped, interrupted
// or finished worker's task is not started again without the user. Saved
// workers of an earlier process count too.
func (a *AgentTool) repeatedBrief(ctx context.Context, agentName, prompt string) string {
	if id := a.runningDuplicate(agentName, prompt); id != "" {
		return fmt.Sprintf("refused: %s is already working on this exact task in this conversation; wait for its report (it arrives automatically) or use send_message mode=steer to change it", id)
	}
	if a.ParentLoop == nil || !a.ParentLoop.wakeTurnForRun {
		return ""
	}
	// An automatic turn starts new workers only when the user allows it
	// (background_wake_delegate, on by default: the next planned task goes to
	// a freed slot).
	if s := a.ParentLoop.WakeScheduler(); s == nil || !s.Config().Delegate {
		return "refused: this is an automatic turn without a user message; it does not start new workers. " +
			"Report the outcome and propose the next step; the user decides (or enables background_wake_delegate for unattended refills)."
	}
	// Even then it never repeats a worker of this conversation that failed,
	// was interrupted or stopped - matched by goal and task, not wording (a
	// rephrased retry is still a retry) - nor starts a worker's exact task
	// again while it runs or after it finished.
	check := func(id, agent, desc, status string) string {
		if agent != agentName {
			return ""
		}
		exact := strings.Join(strings.Fields(desc), " ") == strings.Join(strings.Fields(prompt), " ")
		switch status {
		case "failed", "stopped", WorkerStatusInterrupted:
			if sameTask(desc, prompt) {
				return fmt.Sprintf("refused: this automatic turn (no user message) would repeat %s's task (%s). Failed or stopped tasks are not delegated again automatically: report its outcome and let the user decide; the user resumes it with the Resume button or a message naming it.", id, status)
			}
		case "running", "created":
			if exact {
				return fmt.Sprintf("refused: %s is already working on this task. Do not start it twice; its report will start an automatic turn.", id)
			}
		default:
			if exact {
				return fmt.Sprintf("refused: this automatic turn (no user message) would redo %s's finished task (%s). Use its report instead of starting it again.", id, status)
			}
		}
		return ""
	}
	for _, s := range a.conversationWorkers() {
		if r := check(s.ID, s.Agent, s.Description, s.Status); r != "" {
			return r
		}
	}
	sid := sessionIDOf(a.ParentLoop)
	if lister, ok := a.Workers.Store().(workerBriefLister); ok && sid != "" {
		if recs, err := lister.ListWorkerBriefs(ctx, sid); err == nil {
			for _, rec := range recs {
				if r := check(rec.ID, rec.Agent, rec.Description, rec.Status); r != "" {
					return r
				}
			}
		}
	}
	return ""
}

// runningDuplicate returns the id of this conversation's worker that is
// already running (or queued) the same agent with the same brief, or "".
// A model that sends one task twice (in one response or across turns) would
// otherwise run the same work twice, in parallel, on the same files.
func (a *AgentTool) runningDuplicate(agentName, prompt string) string {
	want := strings.Join(strings.Fields(prompt), " ")
	if want == "" || a.ParentLoop == nil {
		return ""
	}
	for _, s := range a.conversationWorkers() {
		if (s.Status == "running" || s.Status == "created") && s.Agent == agentName && strings.Join(strings.Fields(s.Description), " ") == want {
			return s.ID
		}
	}
	return ""
}

// savedWorkerStatus returns the status of a worker of this conversation that
// is not in the live registry: one saved by an earlier process (restored from
// the database after a restart). "" when unknown.
func (a *AgentTool) savedWorkerStatus(ctx context.Context, id string) string {
	if a == nil || a.Workers == nil {
		return ""
	}
	if w, ok := a.Workers.Get(id); ok {
		return w.status()
	}
	store := a.Workers.Store()
	sid := sessionIDOf(a.ParentLoop)
	if store == nil || sid == "" {
		return ""
	}
	rec, ok, err := store.LoadWorker(ctx, sid, id)
	if err != nil || !ok {
		return ""
	}
	return rec.Status
}

// conversationWorkersWithSaved adds this conversation's workers that exist
// only in the database (saved by an earlier process) to the live ones, so a
// status answer and the continuation guard see them after a restart.
func (a *AgentTool) conversationWorkersWithSaved() []Snapshot {
	live := a.conversationWorkers()
	sid := sessionIDOf(a.ParentLoop)
	lister, ok := a.Workers.Store().(workerBriefLister)
	if !ok || sid == "" {
		return live
	}
	recs, err := lister.ListWorkerBriefs(context.Background(), sid)
	if err != nil {
		return live
	}
	seen := make(map[string]bool, len(live))
	for _, s := range live {
		seen[s.ID] = true
	}
	for _, rec := range recs {
		if !seen[rec.ID] {
			live = append(live, Snapshot{ID: rec.ID, Agent: rec.Agent, Description: rec.Description, Status: rec.Status, SessionID: sid,
				CommandsRun: rec.CommandsRun, ChecksRun: rec.ChecksRun})
		}
	}
	return live
}

// conversationWorkers returns snapshots of the parent conversation's workers
// in the live registry (the registry is process-wide in the GUI).
func (a *AgentTool) conversationWorkers() []Snapshot {
	if a == nil || a.Workers == nil {
		return nil
	}
	sid := sessionIDOf(a.ParentLoop)
	var out []Snapshot
	for _, w := range a.Workers.List() {
		s := w.Snapshot()
		if s.SessionID == sid {
			out = append(out, s)
		}
	}
	return out
}

// Spec returns the tools.Tool description. The schema lists
// the registered sub-agents in the `enum` of the `agent`
// parameter so the model cannot pick a name that does not
// exist.
func (a *AgentTool) Spec() tools.Tool {
	names := a.Registry.Names()
	enumJSON, _ := json.Marshal(names)
	adviseProp := `
				"advise": {"type": "boolean", "description": "optional: READ-ONLY second opinion (advisor worker; never edits files). Use before choosing an approach for a hard decision, after 2 failed attempts at the same fix, or before declaring a risky change done"},`
	if a.AdvisorOff {
		adviseProp = ""
	}
	return tools.Tool{
		Name: "task",
		Description: "Delegate independent work or substantial exploration to an isolated worker. " +
			"Use direct tools for targeted lookups; send_message to reuse a worker. " +
			"Provide goal, paths/findings and expected result. Relevant already-read source excerpts are attached automatically; other chat history is private. " +
			"Returns a report and evidence; use these to finish the user's task. Background results resume this run automatically: end the response to wait, without polling or restarting pending work. Workers cannot delegate.",
		Schema: fmt.Sprintf(`{
			"type": "object",
			"properties": {
				"prompt": {"type": "string", "description": "self-contained briefing for the worker"},
				"expect": {"type": "string", "description": "optional: what the report must contain"},
				"agent":  {"type": "string", "enum": %s, "description": "optional worker kind; omit for a general worker"},%s
				"async": {"type": "boolean", "description": "optional: run in the background and return a worker ID; completion arrives as a task notification"}
			},
			"required": ["prompt"]
		}`, string(enumJSON), adviseProp),
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
