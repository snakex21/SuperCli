package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"supercli/internal/agent/ultrawork"
	"supercli/internal/llm"
	"supercli/internal/llm/draft"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
)

// SessionWriter is the minimal contract the loop needs to
// persist messages as they are produced. It is implemented by
// session.Store in F2.c. The interface lives here so that the
// agent package does not depend on the session package.
type SessionWriter interface {
	// AppendMessage is called exactly once per message the
	// loop adds to l.Messages. Implementations must be safe
	// for concurrent use.
	AppendMessage(ctx context.Context, msg llm.Message) error
	// UpdateUsage is called once per turn with the cumulative
	// tokens spent in that turn. (F2.c uses additive
	// accumulation.)
	UpdateUsage(in, out int) error
}

// contextProjectionWriter is an optional extension implemented by the
// SQLite session writer. It stores the exact provider-visible view while
// the ordinary writer retains the full transcript.
type contextProjectionWriter interface {
	SaveContextProjection(ctx context.Context, msgs []llm.Message) error
}

// SessionReader is the resume contract. F2.c reads the
// conversation history back as []llm.Message.
type SessionReader interface {
	ReadMessages(ctx context.Context) ([]llm.Message, error)
}

// Loop is the production Agent. It streams model output, executes
// tool calls, feeds the results back, and repeats until the model
// emits a "stop" finish reason or MaxSteps is hit.
type Loop struct {
	provider      llm.Provider
	registry      *tools.Registry
	caps          *llm.CapabilityRegistry
	system        string
	briefing      string
	maxSteps      int
	maxStepGrace  int
	thinTools     bool
	stableToolset bool
	catalogHoist  bool
	orchestrator  bool
	// taskParallel decides whether a batch of multiple `task` calls in
	// one model turn runs concurrently. Resolved by the app layer:
	// parallel for cloud backends, sequential for local ones (one GPU
	// slot serializes them anyway and interleaved contexts thrash the
	// KV cache), with a config override. taskParallelWarnLocal is set
	// when the resolution is "parallel on a local backend" (forced) so
	// the loop can warn once at execution time. taskParallelWarned
	// guards that one-shot warning.
	taskParallel          bool
	taskParallelWarnLocal bool
	taskParallelWarned    bool
	thinHintMax           int
	// hoistedPre freezes the thin-tools preamble bytes for the whole
	// session when stableToolset is on: the preamble then lives in the
	// stable system prefix (paid once, KV-cached) instead of being
	// re-injected — and re-evaluated by llama.cpp — behind the growing
	// history every step. hoistedPreSet gates the lazy first render so
	// the freeze happens after any SetRegistry swap. Guarded by nothing:
	// providerMessages runs on the loop goroutine only.
	hoistedPre    string
	hoistedPreSet bool
	baseDir       string
	writer        SessionWriter
	// persistHealth tracks session-write reliability: sticky
	// first error, failure counter, in-order retry buffer and
	// the one-shot UI warning. See persist_health.go.
	persistHealth   persistHealth
	errorLog        ErrorLogger
	reflector       Reflector
	reflectEvery    int
	adaptiveReflect bool
	patternInjector PatternInjector
	creditTracker   CreditTracker
	modelID         string

	// sessUsage accumulates provider-reported token usage across
	// every Run of this loop (the whole TUI session). Guarded by
	// sessUsageMu because /context reads it from the TUI goroutine.
	sessUsage   Usage
	sessUsageMu sync.Mutex

	// lastTurn* hold the provider-reported accounting for the most
	// recent turn (not cumulative), for the status-line cache-hit% and
	// reasoning-token badges. Guarded by sessUsageMu. lastTurnSet flips
	// true after the first turn that carried provider usage.
	lastTurnPrompt    int
	lastTurnCached    int
	lastTurnOutput    int
	lastTurnReasoning int
	lastTurnSet       bool
	// contextBase* calibrate the cheap next-request estimate with the most
	// recent provider-reported prompt usage. The calibration is additive only
	// while the estimated request grows; if history is compacted/pruned/hidden
	// and the estimate shrinks, window.go deliberately falls back to the raw
	// estimate until the provider supplies a fresh baseline.
	contextBaseExact     int
	contextBaseEstimated int
	contextBaseRoute     RouteMode

	// Auto-compact wiring (wave 4). windowFor resolves the
	// model's context window (config > provider metadata >
	// learned > default); summarizer produces the /compact
	// summary; learnLimit persists a limit discovered from a
	// provider context-length error.
	windowFor        func(model string) int
	contextWindowFor func(model string) ContextWindowResolution
	contextProvider  string
	scopedWindowFor  func(provider, model string) ContextWindowResolution
	summarizer       Summarizer
	learnLimit       func(model string, limit int)
	// pruneProtect: tool-result tokens protected from pruning
	// (prune.go). 0 = defaultPruneProtectTokens, negative = prune
	// disabled.
	pruneProtect int

	// F9 ultrawork wiring. When non-nil, the loop:
	//   - detects the "ultrawork"/"ulw" keyword in the user
	//     prompt and (if the gates pass) flips ultraworkMode
	//     to true and injects the ultrawork system-prompt
	//     section
	//   - runs the Sisyphus enforcer at the end of every
	//     turn; when it returns (true, reminder) the loop
	//     re-prompts the model instead of emitting DoneEvent
	ultraworkGates    *ultrawork.Wiring
	ultraworkSisyphus *ultrawork.Sisyphus
	ultraworkMode     bool

	// F11 draft wiring. The bridge calls a cheap
	// "draft" model before each verifier call when
	// the policy says so; the draft's plan is
	// injected as a system message so the verifier
	// sees it. bridge is non-nil only when both
	// cfg.Draft is non-nil and cfg.DraftProvider
	// is non-nil.
	draftBridge  *draft.Bridge
	draftPolicy  *draft.Policy
	draftSavings *draft.Savings
	// Per-step tool names we are about to invoke
	// (used to decide CriticalOnly drafts before
	// the actual tool execution). Populated per
	// iteration from the verifier's toolCalls slice.
	upcomingTools []string
	// invoke_tool dispatch counters. A resolved envelope is rewritten to
	// its target before anything is recorded, so successes leave no trace
	// while failures do — the dispatcher's hit rate was unmeasurable.
	// invokeDispatchStep is the current step's count (loop goroutine only,
	// reset per step); invokeDispatchTotal is the session total and is read
	// from outside the loop goroutine.
	invokeDispatchStep  int
	invokeDispatchTotal atomic.Int64
	// Last draft plan we injected, kept so we can
	// compare to the verifier's first text response
	// to compute savings / detect overrides.
	lastDraftText     string
	lastDraftTokens   int
	draftOverrideSink DraftOverrideSink
	stats             stats.Recorder
	// toolEvidence is reset for each user Run. A model-issued positive goal
	// verification must follow at least one successful, concrete tool result in
	// that same foreground run; manual UI verification bypasses the agent loop.
	toolEvidence atomic.Bool
	// concreteFailure is true when the latest concrete tool result failed. Goal
	// task completion and passing verification are held back until the model
	// performs another successful concrete action. This prevents a red test or
	// failed write from being immediately declared complete.
	concreteFailure atomic.Bool

	// stepPhaseWall accumulates the DISJOINT wall-clock phases of the
	// current step (context_prepare, request_encode, backend_wait,
	// stream_total, tool_execution) so statsEndStep can attribute the
	// remainder of the step to next_turn_prepare. Loop-goroutine only —
	// overlapping phases recorded from worker goroutines (per-tool
	// timings, session_persist) go straight to the recorder and are
	// deliberately kept out of this sum, otherwise the remainder would
	// double-count and go negative. Reset at every statsStartStep.
	stepPhaseWall time.Duration
	// stepAuxWall accumulates the wall time of model-powered AUX
	// operations inside the current step (draft, auto-compact
	// summary, reflection). Each is recorded as its own
	// "model:<purpose>" phase and added to stepPhaseWall, so
	// context_prepare and next_turn_prepare keep measuring PURE
	// CLI overhead — hidden inference no longer inflates them.
	// Loop-goroutine only; reset at every statsStartStep.
	stepAuxWall time.Duration

	// F14 selective context deletion. The hidden
	// shadow slice has the same length as Messages;
	// a true entry means the corresponding message
	// is replaced by a placeholder in the provider's
	// view (VisibleMessages) but remains in Messages
	// (and remains persisted + searchable via F13).
	// Reset on Run start.
	hidden []bool

	// extOut is an optional channel for events
	// that don't belong to a single Run (F12
	// ConsultEvent from a /council slash command
	// or a consult tool call). Set by main.go via
	// SetExternalSink; never closed by the loop.
	// Emit is non-blocking.
	extOut chan<- Event

	// routeMap selects a cheap per-run provider view. Chat-only mode removes
	// tool schemas and the full coordinator prompt for simple conversation.
	routeMap RouteMap
	route    RouteMode
	navigate bool
	// navAuto (only meaningful with navigate) makes routing keyword-first:
	// a confident RouteMap hit skips the extra navigator model round-trip;
	// only ambiguous prompts fall back to the model navigator.
	navAuto bool
	// navKeywordsOnly uses the safe coordinator fallback for ambiguous
	// prompts instead of spending an extra call on the main model.
	navKeywordsOnly bool
	// navProvider (optional) runs the navigator's route classification
	// on a small side provider instead of the main one. On a llama.cpp
	// host with a single slot the navigator prompt (different prefix)
	// evicts the coordinator's KV cache, forcing a full prefill re-eval
	// on the next call — routing it to another model/host avoids that.
	// nil = classify on the main provider (historical behaviour). Any
	// error still degrades to the keyword RouteMap fallback.
	navProvider llm.Provider

	// nextUserAddon is appended ONCE to the next Run's user message
	// (then cleared). Used by the preflight repo-context block
	// (config preflight_repo): the block rides the VARIABLE side of
	// the prompt — a user message — never the system prefix, so the
	// stable KV-cache front is untouched. Set via SetNextUserAddon.
	nextUserAddon string
	// nextUserImages are delivered directly with the next user message. They
	// remain available across all provider/tool steps of that Run, then their
	// heavy base64 payload is removed before a later Run can reuse it.
	nextUserImages []llm.ImageRef
	// nextCoordinatorAddon is the route-aware variant used by repository
	// preflight. It waits until a coordinator turn, so greetings and general
	// advice do not pay hundreds of irrelevant repository tokens. Unlike the
	// user's actual message it is ephemeral and is not persisted to history.
	nextCoordinatorAddon string

	// interjections are user messages typed while a Run is active. The TUI
	// enqueues from Bubble Tea's goroutine; the loop drains only between model
	// steps, so history mutation remains single-owner and tool-call/result pairs
	// can never be split. The small cap prevents an unattended UI from growing
	// an unbounded side queue.
	interjectionMu sync.Mutex
	interjections  []string

	// chatWindowStart is the sticky start (a VisibleMessages index) of
	// the growing history window used by the light routes (chat-only /
	// advisor / clarify). It only ever moves FORWARD, in one big jump,
	// when the window outgrows chatWindowMaxTokens — so between jumps
	// each light-route request is a strict append to the previous one
	// and the provider-side KV prompt cache gets full prefix hits.
	// Reset to 0 whenever the conversation body is rebuilt
	// (LoadConversation, CompactWithSummary).
	chatWindowStart int

	// identicalFails blocks a third identical failed tool call in a Run.
	identicalFails identicalFailureGate

	// identicalWrites blocks an identical successful mutation that has
	// already been applied repeatedMutationLimit times in a Run.
	identicalWrites identicalSuccessGate

	// emptyReplyNudgeUsed is set when we already forced one "answer the
	// user after tools" retry in this Run (avoids an infinite empty loop).
	emptyReplyNudgeUsed bool

	// forceReplyWithoutTools: next provider Complete gets toolDefs=nil so
	// the model physically cannot call tools (prompt-only is not enough).
	// Cleared after that one Complete, so the following user turn is normal.
	forceReplyWithoutTools bool

	// Messages is the running conversation. The loop appends to
	// it on every turn so the model sees the full history.
	Messages []llm.Message
	// visibleEstimate caches the exact estimate of the append-only,
	// fully-visible prefix. Most sessions never hide messages, so each
	// step prices only the newly appended tail instead of rescanning the
	// entire conversation. Rewrites explicitly invalidate the cache.
	visibleEstimateCount  int
	visibleEstimateTokens int
}

// chatWindowMaxTokens is the estimated-token threshold of the light-route
// history window. Light routes are cheap smalltalk prompts, so the window
// is kept small; but cutting it must be RARE and BIG — a per-turn trim
// would make the prompt non-append-only and cap KV-cache reuse by
// construction. chars/4 estimate, history only (the current turn is
// always sent in full).
const chatWindowMaxTokens = 3000

// chatWindowKeepMsgs is how many eligible messages survive a window jump.
const chatWindowKeepMsgs = 4

// chatWindowEligible mirrors the historical light-route tail filter:
// only plain user/assistant text — no system messages, no background
// task notifications, no tool-call turns — may leak into smalltalk.
func chatWindowEligible(m llm.Message) bool {
	if m.Role != llm.RoleUser && m.Role != llm.RoleAssistant {
		return false
	}
	if strings.Contains(m.Content, "<task-notification>") || len(m.ToolCalls) > 0 {
		return false
	}
	return true
}

// Reflector is the F5.a hook the loop calls every ReflectEvery
// steps. It returns the reflection text that will be appended
// to Messages as a system message before the next model call.
// Returning ("", nil) is a valid no-op.
//
// The interface lives in the agent package (not reflect) so
// the agent package does not depend on the reflect package.
// main.go wires the concrete reflect.ModelReflector.
type Reflector interface {
	Reflect(ctx context.Context, history []llm.Message) (string, error)
}

// PatternInjector is the F5.d hook. Build returns text to
// append to the system message at session start. Empty
// result is a no-op.
type PatternInjector interface {
	Build(ctx context.Context, systemContext string) (string, error)
}

// ErrorLogger is the F4.d hook the loop uses to record
// classified tool errors. It is satisfied by
// tools.ErrorLog; we keep the interface here to avoid a
// direct dependency from agent → tools.ErrorLog (test
// stubs can pass a no-op).
type ErrorLogger interface {
	Append(r tools.ErrorRecord)
}

// LoopConfig is the constructor input.
type LoopConfig struct {
	Provider llm.Provider
	Registry *tools.Registry
	// Caps, when non-nil, is used by the loop to decide if a
	// produced image needs to be re-encoded or dropped. When nil,
	// vision is assumed (matches what the provider was configured
	// with).
	Caps *llm.CapabilityRegistry
	// System is the system prompt prepended on every run. Empty
	// is fine.
	System string
	// Briefing is the code-built memory briefing (user preferences,
	// project card, recent sessions). The coordinator route already
	// sees it inside System; chat/advisor routes replace System with
	// a minimal prompt, so the loop re-appends Briefing there — the
	// model must know durable user facts even in smalltalk.
	Briefing string
	// MaxSteps caps the number of model calls in a single Run.
	// Zero means default (10). Negative means no cap (dangerous).
	MaxSteps int
	// MaxStepGrace makes MaxSteps a soft limit while successful, non-repeated
	// tool batches show concrete progress. The loop extends in small chunks up
	// to MaxSteps+MaxStepGrace. Zero keeps MaxSteps as a strict hard cap.
	MaxStepGrace int
	// InitialMessages seeds the conversation. Used for tests and
	// for resuming a session. The session writer, if any, is
	// NOT called for these.
	InitialMessages []llm.Message
	// EnableNavigator turns on the cheap pre-request model router that chooses
	// chat/advisor/coordinator. Main SuperCli enables it; child workers and most
	// tests leave it off to avoid extra provider calls.
	EnableNavigator bool
	// NavigatorAuto (only meaningful with EnableNavigator) makes routing
	// keyword-first: a confident RouteMap classification is used directly,
	// and only ambiguous prompts pay for the navigator model round-trip.
	// Off = the navigator model runs every user turn (historical behaviour).
	NavigatorAuto bool
	// NavigatorKeywordsOnly (with NavigatorAuto) never calls a model merely
	// to select a route. Ambiguous prompts safely use coordinator mode.
	NavigatorKeywordsOnly bool
	// ThinTools enables the thin tool protocol on the coordinator
	// route: only thinCoreTools carry a full JSON Schema each turn;
	// the rest are advertised in a compact name+hint catalog and
	// pulled in on demand via tool_search. Default false preserves
	// the historical behaviour (every visible tool, full schema).
	// Intended for small/local models where schema bulk dominates
	// the prefill cost.
	ThinTools bool
	// StableToolset (only meaningful with ThinTools) keeps the
	// request `tools` list fixed for the whole session: a tool
	// activated via tool_search is NOT promoted into the
	// schema-carrying set. Its schema still reaches the model as
	// the tool_search result text (cache-safe, end of history) and
	// Registry.Execute runs it by name regardless of promotion.
	// Chat templates serialize `tools` at the very start of the
	// prompt, so keeping the list stable preserves the server-side
	// KV prompt cache across activations instead of invalidating
	// it on every tool load. Default false = historical behaviour
	// (activated tools get promoted, tools list grows).
	StableToolset bool
	// CatalogHoist (only meaningful with ThinTools+StableToolset)
	// moves the thin-tools preamble (sentinel instruction + dormant
	// catalog) from its per-request tail position into the stable
	// system prefix. At the tail its position shifts every step as
	// history grows, so llama.cpp re-evaluates the whole catalog
	// (hundreds–1500 tok) on every model call; hoisted, it sits in
	// the KV-cached prefix and is paid once per session. The rendered
	// bytes are frozen on first use so the prefix stays byte-stable.
	// Default false: the catalog keeps its recency placement until
	// the hoist is live-verified (risk: a small model may reach for
	// tool_search less reliably when the catalog is not at the end).
	CatalogHoist bool
	// Orchestrator enables the HARD delegation mode. It does not by
	// itself restrict the registry (the app layer passes a restricted
	// registry via OrchestratorRegistry); it only tells the loop to
	// treat `task` as a schema-carrying thin-core tool so delegation is
	// directly callable with a full schema from turn 1. Default false.
	Orchestrator bool
	// TaskParallel makes a batch of multiple `task` delegations emitted
	// in one model turn run concurrently. Default false = sequential
	// (safe on a single local GPU, where concurrent worker contexts
	// serialize on one server slot and thrash each other's KV cache).
	// The app layer resolves this from the backend (parallel for cloud,
	// sequential for local) with a config override (task_parallel).
	// A single `task` call is always run inline regardless.
	TaskParallel bool
	// TaskParallelWarnLocal, when true, tells the loop the parallel
	// decision was FORCED onto a local backend; it emits a one-shot
	// NoticeEvent (KV-cache thrash, ~N× time) the first time it actually
	// runs a parallel task batch. Only meaningful with TaskParallel.
	TaskParallelWarnLocal bool
	// ThinHintMax caps each catalog hint length in runes. Zero falls
	// back to defaultThinHintMax. Only consulted when ThinTools is on.
	ThinHintMax int
	// BaseDir is the project home that relative tool paths resolve
	// against. Passed to the verifier so file checks stat the actual
	// written file (home/path) rather than CWD/path. Empty keeps the
	// legacy CWD-relative verification.
	BaseDir string
	// Writer, when non-nil, is invoked once per message the loop
	// appends to Messages. Use session.Store from F2.c.
	Writer SessionWriter
	// ErrorLog, when non-nil, receives one F4.d-classified
	// record per failed tool call. The loop does not block
	// on this write; failures are silent.
	ErrorLog ErrorLogger
	// Reflector, when non-nil, can inject a self-review before the next
	// provider call. AdaptiveReflection makes this signal-driven so a
	// healthy local-model run does not pay for a periodic extra inference.
	Reflector Reflector
	// ReflectEvery controls the legacy fixed interval. A value <= 0
	// disables fixed checkpoints; AdaptiveReflection may still trigger
	// unless the app also omits Reflector.
	ReflectEvery int
	// AdaptiveReflection fires only after deterministic signs of trouble:
	// repeated tool failures, an identical tool-call batch, or the final
	// useful checkpoint before MaxSteps. It is the app default; the bool is
	// explicit so tests and embedders retain the legacy fixed contract.
	AdaptiveReflection bool
	// PatternInjector, when non-nil, contributes a section
	// to the system message at the start of every Run. F5.d.
	PatternInjector PatternInjector
	// CreditTracker, when non-nil, records per-turn
	// token usage. F7 budgets. A return of
	// credits.ErrBudgetExceeded short-circuits the run
	// with an ErrorEvent.
	CreditTracker CreditTracker
	// Ultrawork, when non-nil, enables F9 ultrawork mode.
	// The loop will:
	//   - detect the "ultrawork"/"ulw" keyword in the user
	//     prompt and (gates permitting) flip into
	//     full-autonomy mode
	//   - run the Sisyphus enforcer at the end of every
	//     turn so the model is re-prompted while the
	//     active /goal still has unfinished tasks
	//
	// nil means "F9 disabled", which is the default.
	Ultrawork *ultrawork.Wiring

	// Draft, when non-nil, enables F11 draft-mode
	// bridging. The policy decides WHEN to draft
	// (mode + critical tools) and WHICH model to use.
	// nil means "F11 disabled", which is the default.
	//
	// DraftProvider is the cheap model the bridge
	// calls. nil disables F11 even when Draft is
	// non-nil. main.go picks the draft model from
	// CapabilityRegistry.SuggestCheapestForTask at
	// startup; the loop never picks a model itself.
	Draft         *draft.Policy
	DraftProvider llm.Provider

	// NavigatorProvider, when non-nil, is the small side provider the
	// navigator uses for route classification instead of the main one
	// (see Loop.navProvider). main.go wires the task_model worker
	// provider or the draft provider here when one is configured; the
	// loop never picks a model itself. nil = classify on the main
	// provider.
	NavigatorProvider llm.Provider

	// DraftOverrideSink, when non-nil, receives one
	// record per "verifier overrode the draft" event.
	// The F5 reflector package provides a default
	// JSONL impl (reflect.JSONLDraftOverrideSink)
	// that writes to <home>/.supercli/reflect/.
	// nil = overrides are dropped.
	DraftOverrideSink DraftOverrideSink

	// WindowFor, when non-nil, resolves the context window
	// (tokens) for a model id. <=0 means unknown (the loop
	// falls back to a 16384 default). The callback owns the
	// resolution cascade: config context_window > provider
	// /v1/models metadata > learned limit > default.
	WindowFor func(model string) int

	// ContextWindowFor is the source-aware form of WindowFor. New front-ends
	// should use it so compaction telemetry explains whether a limit came from
	// config, provider metadata, the model catalog, learned errors, or fallback.
	// WindowFor remains supported for embedders and older tests.
	ContextWindowFor func(model string) ContextWindowResolution

	// ContextProvider is the configured connection/profile name owning the
	// active model. ScopedContextWindowFor uses it to keep manual budgets for
	// identical model IDs on different APIs isolated.
	ContextProvider string
	// ScopedContextWindowFor is preferred for provider-scoped manual budgets.
	// It runs before ContextWindowFor and may return zero to continue fallback.
	ScopedContextWindowFor func(provider, model string) ContextWindowResolution

	// Summarizer, when non-nil, enables automatic context
	// compaction: before each provider call, if the visible
	// token estimate enters the reserved generation space, the
	// conversation is summarized and replaced (same
	// machinery as /compact).
	Summarizer Summarizer

	// LearnLimit, when non-nil, is called with the limit
	// extracted from a provider context-length error so it
	// can be persisted per model.
	LearnLimit func(model string, limit int)

	// PruneProtectTokens is the size of the freshest tool-result
	// tail (estimated tokens) protected from the zero-LLM prune
	// pass (prune.go). 0 = window-scaled default. Negative
	// disables pruning entirely.
	PruneProtectTokens int

	// Stats, when non-nil, receives per-step metrics
	// (token counts, draft savings). F2.g's Memory
	// impl is the in-memory default; the persistent
	// path is out of scope for F11.
	Stats stats.Recorder
}

// CreditTracker is the F7 hook. Implementations:
//   - record (in, out) tokens per turn
//   - enforce PerSession/PerDay caps
//
// Returning credits.ErrBudgetExceeded makes the loop
// stop with an error event. Any other error is logged
// but ignored.
type CreditTracker interface {
	Record(ctx context.Context, in, out int64, model string) error
	Used() (session, daily int64)
}

// Name implements Agent.
func (l *Loop) Name() string { return "supercli-loop" }

// Run implements Agent. It appends the prompt as a user message,
// then iterates provider calls + tool executions until the model
// emits a non-tool finish reason, an error occurs, or MaxSteps is
// hit. Events stream over the returned channel in real time.
//
// The channel is closed exactly once on exit. A successful run
// ends with a DoneEvent; a failed run ends with an ErrorEvent.
// Tool calls and their results are interleaved in the stream so
// the TUI can render each step.
func (l *Loop) Run(ctx context.Context, prompt string) (<-chan Event, error) {
	if prompt == "" {
		return nil, fmt.Errorf("agent.Loop.Run: prompt is empty")
	}
	// F14: hidden flags deliberately SURVIVE across Runs. /clear,
	// hide_messages and budget eviction all fire between or during
	// Runs and express durable intent ("this content is out of the
	// model's context"); resetting here made /clear a no-op for the
	// next message and invalidated the KV-cache prefix. Hides are
	// only reset where the message indices themselves become invalid
	// (compaction, LoadConversation).
	out := make(chan Event, 32)

	// F9 ultrawork: detect the keyword in the user prompt.
	// When detected AND the gates pass, switch the loop
	// into full-autonomy mode for the rest of this Run:
	//   - inject the ultrawork system-prompt section so
	//     the model knows the operating rules
	//   - reset the Sisyphus enforcer's counter (so a
	//     previously-stalled run does not poison this one)
	//
	// When the gates fail, we still return a channel — the
	// TUI gets a single ErrorEvent with the reason and the
	// channel closes. We do NOT silently fall back to
	// non-ultrawork mode; the user asked for autonomy and
	// the answer is "not yet, here's why".
	if l.ultraworkGates != nil && ultrawork.Detect(prompt) {
		res := ultrawork.CheckGates(l.ultraworkGates.Goal, l.ultraworkGates.Credit)
		if !res.OK {
			errEvent := ErrorEvent{Err: fmt.Errorf("ultrawork gate failed: %s", res.Reason)}
			out <- errEvent
			close(out)
			return out, nil
		}
		l.ultraworkMode = true
		if l.ultraworkSisyphus != nil {
			l.ultraworkSisyphus.Reset()
		}
		sys := llm.Message{
			Role:    llm.RoleSystem,
			Content: ultrawork.SystemPromptSection(),
		}
		l.Messages = append(l.Messages, sys)
		l.persist(ctx, sys)
	} else {
		// Either F9 is not wired or the keyword was
		// absent. Either way, make sure ultraworkMode is
		// off for this Run so a stale flag from a
		// previous Run cannot trigger Sisyphus.
		l.ultraworkMode = false
	}

	userText := prompt
	// One-shot user-message addon (preflight repo context). Appended
	// to the message CONTENT only — routing/ultrawork detection above
	// still saw the user's raw words. Cleared so later Runs are clean.
	if l.nextUserAddon != "" {
		userText = prompt + "\n\n" + l.nextUserAddon
		l.nextUserAddon = ""
	}
	userMsg := llm.Message{Role: llm.RoleUser, Content: userText}
	transientImageIndex := -1
	if len(l.nextUserImages) > 0 {
		userMsg.Content = ""
		userMsg.Parts = append(userMsg.Parts, llm.ContentPart{Type: llm.PartTypeText, Text: userText})
		for i := range l.nextUserImages {
			image := l.nextUserImages[i]
			userMsg.Parts = append(userMsg.Parts, llm.ContentPart{Type: llm.PartTypeImage, Image: &image})
		}
		l.nextUserImages = nil
		transientImageIndex = len(l.Messages)
	}
	l.Messages = append(l.Messages, userMsg)
	// Addons are one-shot provider context, not transcript content. Persist the
	// user's raw words so reopening a session never exposes internal preflight
	// or rewind-feedback markers in the conversation UI.
	l.persist(ctx, llm.Message{Role: llm.RoleUser, Content: prompt})
	go l.run(ctx, prompt, out, transientImageIndex)
	return out, nil
}

func (l *Loop) run(ctx context.Context, prompt string, out chan<- Event, transientImageIndex int) {
	defer close(out)
	defer func() {
		if transientImageIndex >= 0 && transientImageIndex < len(l.Messages) {
			l.Messages[transientImageIndex] = l.Messages[transientImageIndex].TextOnly()
		}
	}()
	l.toolEvidence.Store(false)
	l.concreteFailure.Store(false)
	l.emptyReplyNudgeUsed = false
	l.forceReplyWithoutTools = false
	// A final run-goroutine retry covers recovery on the last step. The
	// projection is rebuilt from current Messages, never from a stale snapshot.
	// Bound it so a locked database can never delay shutdown indefinitely.
	defer func() {
		retryCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		l.retryDirtyProjection(retryCtx)
	}()
	defer func() {
		if r := recover(); r != nil {
			// Send the panic as an ErrorEvent so the TUI
			// can show it instead of silently crashing.
			select {
			case out <- ErrorEvent{Err: fmt.Errorf("agent panic: %v", r)}:
			default:
			}
		}
	}()
	// A1: navigator + one-shot coordinator addons (preflight, verification).
	// Runs inside the background goroutine so Run() returns immediately.
	l.prepareRunRoute(ctx, prompt)
	totalUsage := Usage{}
	var reflectionProgress adaptiveReflectionProgress
	var discoveryProg discoveryProgress
	l.identicalFails = identicalFailureGate{}
	l.identicalWrites = identicalSuccessGate{}
	// F11: reset the policy's per-Run "drafted" set
	// at the start of every Run so a ModeBalanced
	// "draft once" rule applies to THIS Run, not to
	// the lifetime of the loop. The set lives in
	// the policy struct (shared by reference).
	if l.draftPolicy != nil {
		l.draftPolicy.Drafted = &map[int]struct{}{}
	}
	stepLimit := l.maxSteps
	hardStepLimit := l.maxSteps
	if l.maxSteps > 0 && l.maxStepGrace > 0 {
		hardStepLimit += l.maxStepGrace
	}
	var limitProgress stepLimitProgress
	for step := 0; step < stepLimit; step++ {
		switch l.runStep(ctx, step, out, &totalUsage, &reflectionProgress, &limitProgress, &discoveryProg, &stepLimit, hardStepLimit) {
		case stepContinue:
			continue
		case stepDone, stepAbort:
			return
		}
	}
	out <- ErrorEvent{
		Err:   fmt.Errorf("agent: max steps (%d) reached", stepLimit),
		Usage: totalUsage,
		Steps: stepLimit,
	}
}

// completeOnce performs one provider Complete call and
// consumes the stream. Split out so the run loop can retry
// once after a context-overflow compaction.
