package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"supercli/internal/agent/ultrawork"
	"supercli/internal/llm"
	"supercli/internal/llm/draft"
	"supercli/internal/llm/prompt"
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

	// Auto-compact wiring (wave 4). windowFor resolves the
	// model's context window (config > provider metadata >
	// learned > default); summarizer produces the /compact
	// summary; learnLimit persists a limit discovered from a
	// provider context-length error.
	windowFor  func(model string) int
	summarizer Summarizer
	learnLimit func(model string, limit int)
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

	// Summarizer, when non-nil, enables automatic context
	// compaction: before each provider call, if the visible
	// token estimate exceeds 80% of the window, the
	// conversation is summarized and replaced (same
	// machinery as /compact).
	Summarizer Summarizer

	// LearnLimit, when non-nil, is called with the limit
	// extracted from a provider context-length error so it
	// can be persisted per model.
	LearnLimit func(model string, limit int)

	// PruneProtectTokens is the size of the freshest tool-result
	// tail (estimated tokens) protected from the zero-LLM prune
	// pass (prune.go). 0 = built-in default (8192). Negative
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

// NewLoop returns a configured Loop. Provider and Registry are
// required; an error is returned if either is nil.
func NewLoop(cfg LoopConfig) (*Loop, error) {
	if cfg.Provider == nil {
		return nil, fmt.Errorf("agent.NewLoop: provider is nil")
	}
	if cfg.Registry == nil {
		return nil, fmt.Errorf("agent.NewLoop: registry is nil")
	}
	cfg.Registry.EnsureReadOutput()
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = 10
	}
	msgs := make([]llm.Message, 0, len(cfg.InitialMessages)+4)
	if cfg.System != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: cfg.System})
	}
	msgs = append(msgs, cfg.InitialMessages...)

	loop := &Loop{
		provider:              cfg.Provider,
		registry:              cfg.Registry,
		caps:                  cfg.Caps,
		system:                cfg.System,
		briefing:              cfg.Briefing,
		maxSteps:              cfg.MaxSteps,
		thinTools:             cfg.ThinTools,
		stableToolset:         cfg.StableToolset,
		catalogHoist:          cfg.CatalogHoist,
		orchestrator:          cfg.Orchestrator,
		taskParallel:          cfg.TaskParallel,
		taskParallelWarnLocal: cfg.TaskParallelWarnLocal,
		thinHintMax:           cfg.ThinHintMax,
		baseDir:               cfg.BaseDir,
		writer:                cfg.Writer,
		errorLog:              cfg.ErrorLog,
		reflector:             cfg.Reflector,
		reflectEvery:          cfg.ReflectEvery,
		adaptiveReflect:       cfg.AdaptiveReflection,
		patternInjector:       cfg.PatternInjector,
		creditTracker:         cfg.CreditTracker,
		modelID:               cfg.Provider.Name(),
		windowFor:             cfg.WindowFor,
		summarizer:            cfg.Summarizer,
		learnLimit:            cfg.LearnLimit,
		pruneProtect:          cfg.PruneProtectTokens,
		Messages:              msgs,
		routeMap:              DefaultRouteMap(),
		route:                 RouteCoordinator,
		navigate:              cfg.EnableNavigator,
		navAuto:               cfg.NavigatorAuto,
		navKeywordsOnly:       cfg.NavigatorKeywordsOnly,
		navProvider:           cfg.NavigatorProvider,
		// Phase telemetry rides the same recorder (and the same
		// default-on wiring) as the historical per-turn stats — it is
		// no longer gated on the F11 draft bridge being configured.
		stats: cfg.Stats,
	}

	// F9 ultrawork wiring. We build the Sisyphus enforcer
	// once at construction time and Reset() it at the
	// start of every Run. The gates live in the Wiring
	// itself; we hold a pointer so the loop can call
	// CheckGates directly.
	if cfg.Ultrawork != nil {
		loop.ultraworkGates = cfg.Ultrawork
		loop.ultraworkSisyphus = &ultrawork.Sisyphus{
			Goal:           cfg.Ultrawork.Goal,
			MaxConsecutive: cfg.Ultrawork.SisyphusMax,
		}
	}

	// F11 draft wiring. We build the bridge once at
	// construction time; per-Run the policy's Drafted
	// set is reset. Both Draft and DraftProvider must
	// be non-nil for the bridge to be constructed —
	// half-configured F11 is treated as off.
	if cfg.Draft != nil && cfg.DraftProvider != nil {
		bridge, err := draft.NewBridge(cfg.Draft, cfg.DraftProvider)
		if err == nil {
			loop.draftBridge = bridge
			loop.draftPolicy = cfg.Draft
			loop.draftSavings = draft.NewSavings()
			loop.draftOverrideSink = cfg.DraftOverrideSink
		}
	}

	// F5.d: build the patterns section at session start
	// and append it as a system message. We do this
	// synchronously so the very first provider call sees
	// the patterns. A build error is logged but not fatal.
	if cfg.PatternInjector != nil {
		injection, err := cfg.PatternInjector.Build(context.Background(), cfg.System)
		if err == nil && injection != "" {
			patMsg := llm.Message{Role: llm.RoleSystem, Content: injection}
			loop.Messages = append(loop.Messages, patMsg)
			loop.persist(context.Background(), patMsg)
		}
	}
	return loop, nil
}

// Name implements Agent.
func (l *Loop) Name() string { return "supercli-loop" }

// defaultThinHintMax caps catalog hints when ThinHintMax is 0. 80
// runes proved a good balance in measurement: ~84% token saving vs
// full schemas while keeping each hint a readable sentence.
const defaultThinHintMax = 80

// thinHintMaxOrDefault resolves the per-hint rune cap, falling back
// to defaultThinHintMax when unset. Shared by the catalog renderer
// and the /context accounting so both size the catalog identically.
func (l *Loop) thinHintMaxOrDefault() int {
	if l.thinHintMax <= 0 {
		return defaultThinHintMax
	}
	return l.thinHintMax
}

// SetRegistry swaps the tool registry the loop exposes to the model.
// Used at startup to hand the main loop a restricted (orchestrator)
// registry AFTER every tool — including the late-registered task tools —
// is present in the full base registry. The loop only reads its registry
// per-turn (buildToolDefs), so swapping the pointer before the first Run
// is safe and does not disturb any in-flight state.
func (l *Loop) SetRegistry(r *tools.Registry) {
	if r == nil {
		return
	}
	r.EnsureReadOutput()
	l.registry = r
	// The hoisted thin-tools preamble (stableToolset) renders from the
	// registry; a swap before the first Run must re-render it, not
	// serve a stale frozen copy.
	l.hoistedPreSet = false
	l.hoistedPre = ""
}

// VisibleToolNames returns the names of tools the model can currently see
// (the registry's visible + always-on set). Exposed for tests and
// diagnostics — in orchestrator mode this is the proof that mutating
// tools are physically absent from the main loop's registry.
func (l *Loop) VisibleToolNames() []string { return l.registry.VisibleNames() }

// buildToolDefs assembles the tool definitions sent to the
// provider for the current route. The coordinator route exposes
// every visible tool with its full JSON Schema; chat/advisor
// routes get only the minimal chatRouteTools set (tool_search +
// recall), letting the model pull in more on demand — that
// trimmed set is the per-turn token cost the router avoids.
//
// When thinTools is enabled, the coordinator set is trimmed to the
// full-schema core (thinCoreTools) plus any tool the model already
// pulled in via tool_search; the dormant tail is omitted here and
// advertised in the catalog (see toolCatalog). This is the thin
// tool protocol's token win.
func (l *Loop) buildToolDefs() []llm.ToolDef {
	var toolDefs []llm.ToolDef
	if l.route == RouteCoordinator {
		schema, _ := l.thinPartition()
		for _, t := range schema {
			toolDefs = append(toolDefs, llm.ToolDef{
				Name:        t.Name,
				Description: t.Description,
				Schema:      t.Schema,
			})
		}
		return toolDefs
	}
	for _, name := range chatRouteTools {
		if t, ok := l.registry.Get(name); ok {
			toolDefs = append(toolDefs, llm.ToolDef{
				Name:        t.Name,
				Description: t.Description,
				Schema:      t.Schema,
			})
		}
	}
	return toolDefs
}

// isActivated reports whether name was explicitly pulled in via
// tool_search this session (so it should carry a full schema).
func (l *Loop) isActivated(name string) bool {
	for _, n := range l.registry.ActiveNames() {
		if n == name {
			return true
		}
	}
	return false
}

// thinPartition splits the coordinator's visible tools into the set
// that carries a full JSON Schema this turn (schema) and the dormant
// tail that is advertised in the compact catalog instead (tail).
//
// When thin tools are off it is the identity split: every visible
// tool goes to schema and tail is empty, so callers preserve the
// historical behaviour. A tool is schema-carrying when it is in the
// thin core OR was already activated via tool_search; otherwise it
// is dormant tail. This is the single source of truth for the
// core/tail decision — buildToolDefs, thinToolsPreamble, and the
// /context accounting all derive from it so they cannot drift.
//
// stableToolset changes the activation rule: activated tools stay
// in the tail, so the schema set (and therefore the request `tools`
// list, serialized at the very start of the prompt by chat
// templates) is byte-identical all session and the server-side KV
// prompt cache survives tool activations. The activated tool is
// still fully usable — its schema arrived as the tool_search result
// text and Registry.Execute dispatches by name, not by promotion.
func (l *Loop) thinPartition() (schema, tail []tools.Tool) {
	for _, t := range l.registry.Visible() {
		if l.thinTools && !l.isSchemaCore(t.Name) {
			if l.stableToolset || !l.isActivated(t.Name) {
				tail = append(tail, t)
				continue
			}
		}
		schema = append(schema, t)
	}
	return schema, tail
}

// isSchemaCore reports whether name belongs to the always-full-schema
// core for the active mode. Orchestrator mode uses its own core set
// (delegation-first: task is core, the mutating tools are absent from
// the registry entirely); every other mode uses thinCoreTools.
func (l *Loop) isSchemaCore(name string) bool {
	if l.orchestrator {
		return isOrchestratorCore(name)
	}
	return isThinCore(name)
}

// thinToolsPreamble builds the request-time system block for the
// thin tool protocol: the sentinel call-format instruction (so the
// model knows to write «name\nkey: value» instead of JSON) plus,
// when the dormant tail is non-empty, a compact name+hint catalog
// of tools reachable via tool_search. Returns "" when thin tools
// are off or off the coordinator route, so callers inject nothing.
//
// The instruction is always present under thin tools (it governs
// how even the core tools are called); the catalog is appended
// only when there is a tail to advertise.
func (l *Loop) thinToolsPreamble() string {
	if !l.thinTools || l.route != RouteCoordinator {
		return ""
	}
	out := prompt.ThinToolProtocol

	_, tail := l.thinPartition()
	var direct, loadable []tools.Tool
	for _, tool := range tail {
		if isDirectToolEligible(tool) {
			direct = append(direct, tool)
		} else {
			loadable = append(loadable, tool)
		}
	}
	if body := tools.RenderCatalog(direct, l.thinHintMaxOrDefault()); body != "" {
		out += "\n\nSimple read-only tools, callable now through invoke_tool " +
			"(tool: name, arg.<field>: value):\n" + body
	}
	if len(loadable) > 0 {
		if body := tools.RenderCatalog(loadable, l.thinHintMaxOrDefault()); body != "" {
			out += "\n\nMore tools, loadable on demand — call tool_search with a " +
				"natural-language query to load any (it returns the full schema so " +
				"you can call it the same turn):\n" + body
		}
	}
	return out
}

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

	userMsg := llm.Message{
		Role:    llm.RoleUser,
		Content: prompt,
	}
	// One-shot user-message addon (preflight repo context). Appended
	// to the message CONTENT only — routing/ultrawork detection above
	// still saw the user's raw words. Cleared so later Runs are clean.
	if l.nextUserAddon != "" {
		userMsg.Content = prompt + "\n\n" + l.nextUserAddon
		l.nextUserAddon = ""
	}
	l.Messages = append(l.Messages, userMsg)
	// Addons are one-shot provider context, not transcript content. Persist the
	// user's raw words so reopening a session never exposes internal preflight
	// or rewind-feedback markers in the conversation UI.
	l.persist(ctx, llm.Message{Role: llm.RoleUser, Content: prompt})
	go l.run(ctx, prompt, out)
	return out, nil
}

func (l *Loop) run(ctx context.Context, prompt string, out chan<- Event) {
	defer close(out)
	l.toolEvidence.Store(false)
	l.concreteFailure.Store(false)
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
	// A1: the navigator is a full LLM call. It runs here, inside the
	// background goroutine, so Run() returns immediately and the TUI
	// never blocks on Enter waiting for the route decision.
	switch {
	case !l.navigate:
		// Navigator off: everything is coordinator (safe default for
		// scripted/worker use, which must keep the full tool context).
		l.route = RouteCoordinator
	case l.navAuto:
		// Auto: take the cheap keyword decision on obvious turns and
		// only pay for the navigator model on ambiguous ones. Saves a
		// full model round-trip per confident turn.
		if mode, confident := l.routeMap.ClassifyConfident(prompt); confident {
			l.route = mode
		} else if l.navKeywordsOnly {
			l.route = l.routeMap.Classify(prompt)
		} else {
			l.route = l.navigateRoute(ctx, prompt)
		}
	default:
		l.route = l.navigateRoute(ctx, prompt)
	}
	// Repository context is useful only on the full coordinator route. Keep it
	// queued across chat/advisor turns and attach it to the newest user message
	// immediately before the first coordinator provider call. Routing above saw
	// only the user's raw prompt, and the session store keeps that raw prompt.
	if l.route == RouteCoordinator && l.nextCoordinatorAddon != "" {
		for i := len(l.Messages) - 1; i >= 0; i-- {
			if l.Messages[i].Role == llm.RoleUser {
				l.Messages[i].Content += "\n\n" + l.nextCoordinatorAddon
				l.invalidateVisibleEstimate()
				break
			}
		}
		l.nextCoordinatorAddon = ""
	}
	// Verification is a variable, one-shot user-message hint, never part of the
	// cacheable system prefix. It is injected only for explicit mutation work;
	// project questions and ordinary chat pay zero tokens for it.
	if l.route == RouteCoordinator {
		if hint := implementationVerificationHint(prompt); hint != "" {
			for i := len(l.Messages) - 1; i >= 0; i-- {
				if l.Messages[i].Role == llm.RoleUser {
					l.Messages[i].Content += "\n\n" + hint
					l.invalidateVisibleEstimate()
					break
				}
			}
		}
	}
	totalUsage := Usage{}
	var reflectionProgress adaptiveReflectionProgress
	// F11: reset the policy's per-Run "drafted" set
	// at the start of every Run so a ModeBalanced
	// "draft once" rule applies to THIS Run, not to
	// the lifetime of the loop. The set lives in
	// the policy struct (shared by reference).
	if l.draftPolicy != nil {
		l.draftPolicy.Drafted = &map[int]struct{}{}
	}
	for step := 0; step < l.maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			out <- ErrorEvent{Err: err}
			return
		}
		l.retryDirtyProjection(ctx)

		// Phase telemetry: one stats turn per step. All timings are
		// whole-phase time.Since measurements — nothing is measured
		// per-delta, so the streaming hot path stays allocation-free.
		stepStart := time.Now()
		l.statsStartStep(step + 1)

		// F11 draft step: ask the bridge whether
		// this step should get a draft, and if so,
		// call the draft model and inject the plan
		// as a system message. We do this BEFORE
		// the verifier call so the verifier sees
		// the draft on its first turn.
		//
		// upcomingTools is the verifier's planned
		// tool calls from the PREVIOUS step
		// (zero on step 0). The policy uses it to
		// decide CriticalOnly drafts.
		ok, _ := l.draftPolicy.ShouldDraft(step, l.upcomingTools)
		if l.draftBridge != nil && ok {
			l.invokeDraft(ctx, step, out)
		}

		// Context defense before the provider call, cheap first:
		// (1) prune old tool results in place (zero model calls,
		// prune.go), (2) only if the estimate still crosses 80% of
		// the window, fall back to the summary compaction.
		prepStart := time.Now()
		l.maybePruneToolResults(ctx, out)
		auxBefore := l.stepAuxWall
		l.maybeAutoCompact(ctx, out, "")

		// Build tool definitions from visible tools. Non-coordinator routes
		// get only the minimal chatRouteTools set (tool_search + recall) so
		// the model can pull in more when needed; the full tool list is the
		// actual token cost the router avoids.
		toolDefs := l.buildToolDefs()
		// context_prepare part 1: prune/compact/tool defs. Part 2
		// (provider message assembly) is added inside completeOnce.
		// The auto-compact SUMMARY MODEL CALL is excluded — it is
		// booked as its own model:compact phase (recordAuxWall in
		// maybeAutoCompact), so context_prepare stays pure CLI time.
		prep := time.Since(prepStart) - (l.stepAuxWall - auxBefore)
		if prep < 0 {
			prep = 0
		}
		l.recordWallPhase(stats.PhaseContextPrepare, prep)

		text, toolCalls, usage, err := l.completeOnce(ctx, toolDefs, out)
		if err != nil && l.handleContextOverflow(ctx, err, out) {
			// Wave 4: provider rejected the context size.
			// The learned limit was persisted and the
			// conversation compacted; retry once. The retry's phase
			// timings accumulate onto this step's (documented in
			// stats.Turn.Phases).
			text, toolCalls, usage, err = l.completeOnce(ctx, toolDefs, out)
		}
		if err != nil {
			l.statsEndStep(stepStart)
			out <- ErrorEvent{Err: err}
			return
		}
		// Resolve the schema-stable invoke_tool dispatcher before the
		// assistant/tool-result pair enters history. Valid direct calls become
		// ordinary target calls, so verification, error attribution and
		// telemetry all retain the real tool name.
		toolCalls = l.resolveInvokeToolCalls(toolCalls)
		if usage != nil {
			totalUsage.Input += usage.Input
			totalUsage.Output += usage.Output
			totalUsage.Total += usage.Total
			totalUsage.Cached += usage.CachedInput
			totalUsage.Reasoning += usage.Reasoning
			l.sessUsageMu.Lock()
			l.sessUsage.Input += usage.Input
			l.sessUsage.Output += usage.Output
			l.sessUsage.Total += usage.Total
			l.sessUsage.Cached += usage.CachedInput
			l.sessUsage.Reasoning += usage.Reasoning
			// Last-turn snapshot for the status-line badges.
			l.lastTurnPrompt = usage.Input
			l.lastTurnCached = usage.CachedInput
			l.lastTurnOutput = usage.Output
			l.lastTurnReasoning = usage.Reasoning
			l.lastTurnSet = true
			l.sessUsageMu.Unlock()
			// Provider-reported prompt/completion tokens for the
			// phase telemetry (llama.cpp and the cloud backends
			// report usage on the final delta).
			if l.stats != nil {
				l.stats.RecordTokens(usage.Input, usage.Output)
				l.stats.RecordModel(l.modelID)
			}
			// Report per-turn usage to the writer (if any).
			// Failures feed the persistence-health tracker
			// (sticky first error + /status) but never abort
			// the run.
			if l.writer != nil {
				if err := l.writer.UpdateUsage(usage.Input, usage.Output); err != nil {
					l.persistUsageFailure(err)
				}
			}
			// F7: record to credit tracker. A budget
			// cap is a hard stop, but we still emit
			// the partial usage for the turn.
			if l.creditTracker != nil {
				if err := l.creditTracker.Record(ctx, int64(usage.Input), int64(usage.Output), l.modelID); err != nil {
					l.statsEndStep(stepStart)
					out <- ErrorEvent{Err: err}
					return
				}
			}
			// F11: charge the draft call's tokens
			// against the same tracker. Draft spend
			// shares the user's F7 budget (per D2
			// decision). The draft provider's
			// usage is captured at the end of
			// invokeDraft.
			if l.draftSavings != nil && l.lastDraftTokens > 0 {
				if err := l.recordDraftUsage(ctx); err != nil {
					l.statsEndStep(stepStart)
					out <- ErrorEvent{Err: err}
					return
				}
			}
		}

		// F11: compute the savings / override signal
		// by comparing the verifier's first text to
		// the draft we injected. Emits DraftUsedEvent
		// with the decision and feeds savings into
		// stats + override sink. Must run AFTER
		// consume() so we have the verifier's text
		// and token counts.
		verifyTokens := 0
		if usage != nil {
			verifyTokens = usage.Output
		}
		if l.draftBridge != nil && l.lastDraftText != "" {
			l.recordDraftOutcome(step, text, verifyTokens, out)
		}

		// Build the assistant message and append it to history.
		// Two views: the in-memory history that drives the NEXT
		// provider request keeps only the final answer (reasoning
		// blocks stripped, per Qwen/DeepSeek convention — prior-turn
		// chain-of-thought is not context and re-sending it bloats
		// every subsequent turn). The persisted copy keeps the full
		// text with <thinking> so the UI can replay it. Stripping here
		// (at append time), not in-flight, keeps the cacheable prefix
		// deterministic: the history bytes never change afterwards.
		assistant := llm.Message{Role: llm.RoleAssistant}
		if text != "" {
			assistant.Parts = append(assistant.Parts, llm.ContentPart{Type: llm.PartTypeText, Text: text})
		}
		for _, tc := range toolCalls {
			assistant.ToolCalls = append(assistant.ToolCalls, llm.ToolCall{
				ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments,
			})
		}
		l.persist(ctx, assistant)
		l.Messages = append(l.Messages, stripThinkingFromMessage(assistant))

		// F14: budget-based eviction. After every step we
		// look at the visible token estimate; if it
		// exceeds 80% of the F7 per-session cap, the
		// oldest non-system messages get hidden from the
		// next provider call. Persisted and searchable
		// copies stay intact; only the provider's view
		// is trimmed.
		l.EvictForBudget(ctx, out)

		// F11: capture the names of the tool calls the
		// verifier is about to make so the NEXT
		// iteration's draft policy can decide
		// CriticalOnly. Empty when the model emitted
		// no tool calls (text-only step).
		l.upcomingTools = nil
		for _, tc := range toolCalls {
			l.upcomingTools = append(l.upcomingTools, tc.Name)
		}

		// Tool-calls-per-step is THE parallelism-worthiness metric:
		// count the raw batch size (duplicates included) plus the
		// unique tool names for the per-turn report.
		if l.stats != nil && len(toolCalls) > 0 {
			l.stats.RecordToolCalls(len(toolCalls))
			l.stats.RecordTools(l.upcomingTools)
		}

		if len(toolCalls) == 0 {
			// A user may have typed while this provider call was running. Treat
			// that as the next user turn instead of completing the Run and making
			// them wait/re-submit. Draining here is a safe history boundary: the
			// assistant message above is already complete and persisted.
			if l.drainInterjections(ctx) > 0 {
				l.statsEndStep(stepStart)
				continue
			}
			// F9 Sisyphus: when ultrawork is on AND
			// the active /goal still has unfinished
			// tasks, re-prompt the model instead of
			// emitting DoneEvent. The reminder becomes
			// a system message in the conversation so
			// the next iteration of the loop sees it.
			if l.ultraworkMode && l.ultraworkSisyphus != nil {
				if should, msg := l.ultraworkSisyphus.ShouldContinue(ctx); should {
					sys := llm.Message{Role: llm.RoleSystem, Content: msg}
					l.Messages = append(l.Messages, sys)
					l.persist(ctx, sys)
					// SisyphusEvent uses a separate Hit
					// counter (1-indexed) so the TUI can
					// label it "Sisyphus #1/3" without
					// confusing it with the F5 reflection
					// step.
					out <- SisyphusEvent{
						Step: step + 1,
						Hit:  sisyphusHitFromMessage(msg),
						Text: msg,
					}
					l.statsEndStep(stepStart)
					continue
				}
			}
			l.statsEndStep(stepStart)
			out <- DoneEvent{Usage: totalUsage}
			return
		}

		toolStart := time.Now()
		toolsOK, toolFailures := l.invokeToolCalls(ctx, toolCalls, out)
		l.recordWallPhase(stats.PhaseToolExecution, time.Since(toolStart))
		if !toolsOK {
			l.statsEndStep(stepStart)
			return
		}
		// Tool results have all been appended in deterministic call order. This
		// is the other safe drain point for mid-turn user messages.
		l.drainInterjections(ctx)

		// F5.a: default to signal-driven reflection. A healthy run pays no
		// auxiliary inference merely because it crossed an arbitrary step
		// number. Explicit fixed intervals remain available to embedders and
		// users who set reflect_every=N.
		reason := ""
		if l.adaptiveReflect {
			reason = reflectionProgress.observe(step+1, l.maxSteps, toolCalls, toolFailures)
		} else if l.reflectEvery > 0 && (step+1)%l.reflectEvery == 0 {
			reason = "fixed_interval"
		}
		if reason != "" {
			l.runReflection(ctx, step+1, reason, out)
			reflectionProgress.reset()
		}

		l.statsEndStep(stepStart)
	}
	out <- ErrorEvent{Err: fmt.Errorf("agent: max steps (%d) reached", l.maxSteps)}
}

// invokeToolCalls runs the model's tool-call batch and appends the matching
// tool-result messages to history. Most tools are executed sequentially to
// avoid surprising write conflicts. A batch made only of the coordinator's
// `task` calls can be run concurrently: each task owns an isolated child
// loop/context. Whether it actually runs in parallel is gated by
// taskParallel — on a single local GPU the workers serialize on one server
// slot anyway (N× wall time) and interleaved contexts thrash the KV cache,
// so local backends default to sequential; cloud backends run parallel.
func (l *Loop) invokeToolCalls(ctx context.Context, toolCalls []llm.ToolCall, out chan<- Event) (bool, int) {
	// Independent reads do not touch the model backend and cannot conflict
	// with one another. Run them concurrently on both local and cloud setups;
	// the results are still appended in call order for a stable prompt.
	if len(toolCalls) > 1 && l.allReadOnlyCalls(toolCalls) {
		return l.invokeCallsParallel(ctx, toolCalls, out)
	}
	if len(toolCalls) > 1 && allTaskCalls(toolCalls) && l.taskParallel {
		if l.taskParallelWarnLocal && !l.taskParallelWarned {
			l.taskParallelWarned = true
			out <- NoticeEvent{Text: fmt.Sprintf(
				"running %d task workers in parallel on a local backend — they serialize on one GPU slot (~%d× time) and thrash each other's KV cache; set task_parallel = false for sequential",
				len(toolCalls), len(toolCalls))}
		}
		return l.invokeCallsParallel(ctx, toolCalls, out)
	}

	failures := 0
	for _, tc := range toolCalls {
		ev := l.invoke(ctx, tc, out)
		if ev.failed {
			failures++
		}
		for _, m := range ev.followUps {
			l.Messages = append(l.Messages, m)
			l.persist(ctx, m)
		}
		if ev.fatal {
			out <- ErrorEvent{Err: ev.err}
			return false, failures
		}
	}
	return true, failures
}

func allTaskCalls(toolCalls []llm.ToolCall) bool {
	for _, tc := range toolCalls {
		if tc.Name != "task" {
			return false
		}
	}
	return len(toolCalls) > 0
}

func (l *Loop) allReadOnlyCalls(toolCalls []llm.ToolCall) bool {
	if len(toolCalls) == 0 {
		return false
	}
	for _, call := range toolCalls {
		tool, ok := l.registry.Get(call.Name)
		if !ok || !tool.ReadOnly {
			return false
		}
	}
	return true
}

func (l *Loop) invokeCallsParallel(ctx context.Context, toolCalls []llm.ToolCall, out chan<- Event) (bool, int) {
	type item struct {
		idx int
		res toolResult
	}
	results := make([]toolResult, len(toolCalls))
	ch := make(chan item, len(toolCalls))
	var wg sync.WaitGroup

	for i, tc := range toolCalls {
		i, tc := i, tc
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch <- item{idx: i, res: l.invoke(ctx, tc, out)}
		}()
	}

	wg.Wait()
	close(ch)
	for it := range ch {
		results[it.idx] = it.res
	}

	// Append tool results in the same order as the assistant's tool calls so
	// provider APIs that expect call/result pairing stay deterministic.
	failures := 0
	for _, ev := range results {
		if ev.failed {
			failures++
		}
		for _, m := range ev.followUps {
			l.Messages = append(l.Messages, m)
			l.persist(ctx, m)
		}
		if ev.fatal {
			out <- ErrorEvent{Err: ev.err}
			return false, failures
		}
	}
	return true, failures
}

// completeOnce performs one provider Complete call and
// consumes the stream. Split out so the run loop can retry
// once after a context-overflow compaction.
func (l *Loop) completeOnce(ctx context.Context, toolDefs []llm.ToolDef, out chan<- Event) (string, []llm.ToolCall, *llm.Usage, error) {
	// context_prepare part 2: provider message assembly (visible
	// view, thin preamble placement, freshness stamp).
	msgStart := time.Now()
	msgs := l.providerMessages()
	l.recordWallPhase(stats.PhaseContextPrepare, time.Since(msgStart))

	// request_encode: provider.Complete up to the stream handoff —
	// request serialization plus whatever the provider does before
	// returning its delta channel.
	encStart := time.Now()
	stream, err := l.provider.Complete(ctx, msgs, toolDefs)
	l.recordWallPhase(stats.PhaseRequestEncode, time.Since(encStart))
	if err != nil {
		return "", nil, nil, fmt.Errorf("agent: provider.Complete: %w", err)
	}
	return l.consume(ctx, stream, out)
}

// stampSection is the per-request trailing prompt content: the
// freshness timestamp plus, when the user has turned thinking off for a
// model that honours a prompt soft switch (Qwen /no_think), the
// suppression token. Both live at the very END of the prompt so the
// cacheable prefix is undisturbed — the token is append-only exactly
// like the timestamp, and toggling it never rewrites earlier bytes.
func (l *Loop) stampSection() string {
	s := timeSection(time.Now())
	if d := llm.ThinkingDirective(l.modelID); d != "" {
		s += "\n\n" + d
	}
	return s
}

func (l *Loop) providerMessages() []llm.Message {
	if l.route == RouteCoordinator {
		// Per-request freshness stamp: appended at the END so the stable
		// prompt prefix stays cacheable by the provider.
		visible := l.VisibleMessages()
		out := make([]llm.Message, 0, len(visible)+2)
		// Thin tool protocol placement depends on stableToolset:
		//
		// stableToolset + catalogHoist: the catalog is byte-stable all
		// session (activated tools stay in the tail), so the preamble
		// is HOISTED into the stable prompt prefix — right after the
		// leading run of system messages. There it sits in the
		// server-side KV cache and is evaluated once, instead of being
		// re-injected behind the growing history (a position that
		// shifts every step, forcing llama.cpp to re-eval the whole
		// catalog on every call). The rendered bytes are frozen on
		// first use so late registry changes cannot silently rewrite
		// the prefix. The hoisted message never enters l.Messages, so
		// prune/compaction/token accounting never see it.
		//
		// stableToolset OFF (or hoist not enabled): the catalog can
		// change on activation / recency is preferred, so keep the
		// historical placement — injected just before the freshness
		// stamp at the end of the prompt.
		if l.stableToolset && l.catalogHoist {
			if !l.hoistedPreSet {
				l.hoistedPre = l.thinToolsPreamble()
				l.hoistedPreSet = true
			}
			lead := 0
			for lead < len(visible) && visible[lead].Role == llm.RoleSystem {
				lead++
			}
			// Strict llama.cpp templates commonly accept exactly ONE system
			// message, at index zero. A separate hoisted system message made
			// those templates reject the request before inference. Merge the
			// entire leading system run and the frozen preamble into one stable
			// message; the bytes remain append-only and cacheable, which is the
			// purpose of the hoist in the first place.
			leading := make([]string, 0, lead+1)
			for _, msg := range visible[:lead] {
				if text := messageDraftText(msg); text != "" {
					leading = append(leading, text)
				}
			}
			if l.hoistedPre != "" {
				leading = append(leading, l.hoistedPre)
			}
			if len(leading) > 0 {
				out = append(out, llm.Message{Role: llm.RoleSystem, Content: strings.Join(leading, "\n\n")})
			}
			out = append(out, visible[lead:]...)
		} else {
			out = append(out, visible...)
			if pre := l.thinToolsPreamble(); pre != "" {
				out = append(out, llm.Message{Role: llm.RoleSystem, Content: pre})
			}
		}
		out = append(out, llm.Message{Role: llm.RoleSystem, Content: l.stampSection()})
		return out
	}
	visible := l.VisibleMessages()
	system := chatOnlySystemPrompt
	if l.route == RouteAdvisor || l.route == RouteClarify {
		system = advisorSystemPrompt
	}
	// Memory briefing must survive the route switch: the chat-only
	// prompt replaces the full system prompt, but durable user
	// facts (name, language, preferences) still apply to smalltalk.
	if l.briefing != "" {
		system += "\n\n" + l.briefing
	}
	out := []llm.Message{{Role: llm.RoleSystem, Content: system}}

	// The current turn (everything from the last user message on) is sent
	// verbatim so tool_call/tool_result pairing stays intact when the model
	// uses tool_search or recall on this route.
	lastUser := -1
	for i := len(visible) - 1; i >= 0; i-- {
		if visible[i].Role == llm.RoleUser && !strings.Contains(visible[i].Content, "<task-notification>") {
			lastUser = i
			break
		}
	}
	// Conversational history before the current turn: a GROWING
	// (append-only) window, not a sliding one. A per-turn "last 8" tail
	// rewrote the prompt front every turn, so the provider-side KV
	// cache could never reuse more than the leading system prompt —
	// a construction-level cache killer on these light routes. Instead
	// the window start is sticky (l.chatWindowStart): below the token
	// threshold each turn strictly appends to the previous prompt (full
	// prefix cache hit); once the window outgrows the threshold, the
	// start jumps forward in ONE big leap, keeping only the last
	// chatWindowKeepMsgs messages — the re-eval is paid once per many
	// turns, not every turn ("cut rarely, in big chunks").
	//
	// Eligibility (user/assistant only, no task notifications, no tool
	// calls) is unchanged from the sliding tail: background agent work
	// must not leak into smalltalk. A current turn that used tools is
	// therefore trimmed from history on the NEXT turn — that single
	// divergence point costs one partial re-eval, which is fine (rare
	// on chat routes) and always safe (the server re-evals from the
	// divergence).
	end := len(visible)
	if lastUser >= 0 {
		end = lastUser
	}
	start := l.chatWindowStart
	if start > end {
		// The visible view shrank under us (compaction, /clear).
		// Restart the window; correctness never depends on it.
		start = 0
		l.chatWindowStart = 0
	}
	window := make([]llm.Message, 0, end-start)
	for i := start; i < end; i++ {
		if chatWindowEligible(visible[i]) {
			window = append(window, visible[i])
		}
	}
	if llm.EstimateTokens(window) > chatWindowMaxTokens {
		// One big jump: advance the sticky start so only the last
		// chatWindowKeepMsgs eligible messages stay in the window.
		kept := 0
		ns := end
		for i := end - 1; i >= start && kept < chatWindowKeepMsgs; i-- {
			if chatWindowEligible(visible[i]) {
				kept++
				ns = i
			}
		}
		l.chatWindowStart = ns
		window = window[len(window)-kept:]
	}
	out = append(out, window...)
	if lastUser >= 0 {
		for _, m := range visible[lastUser:] {
			if m.Role == llm.RoleSystem {
				continue
			}
			out = append(out, m)
		}
	}
	// Per-request freshness stamp at the very END, same pattern as the
	// coordinator route: the minute-granular stamp used to be baked into
	// the leading system prompt, rewriting the prompt front every minute
	// and killing the provider-side KV cache. The provider demote pass
	// renders this trailing system message in place as a
	// <system-reminder> user turn.
	out = append(out, llm.Message{Role: llm.RoleSystem, Content: l.stampSection()})
	return out
}

func (l *Loop) navigateRoute(ctx context.Context, prompt string) RouteMode {
	fallback := l.routeMap.Classify(prompt)
	msgs := l.navigatorMessages(prompt)
	// Classification runs on the small side provider when one is wired
	// (task_model worker / draft provider) so the navigator's prompt —
	// a different prefix — never evicts the main conversation from a
	// single-slot llama.cpp KV cache. Errors (including a dead side
	// host) degrade to the keyword fallback, exactly like main-provider
	// errors always have — a broken navigator never breaks the turn.
	prov := l.provider
	if l.navProvider != nil {
		prov = l.navProvider
	}
	stream, err := prov.Complete(llm.WithPurpose(ctx, llm.PurposeNavigator), msgs, nil)
	if err != nil {
		return fallback
	}
	var text strings.Builder
	for d := range stream {
		if d.Err != nil {
			return fallback
		}
		text.WriteString(d.Content)
	}
	mode, ok := parseNavigatorMode(text.String())
	if !ok {
		return fallback
	}
	return mode
}

func (l *Loop) navigatorMessages(prompt string) []llm.Message {
	visible := l.VisibleMessages()
	out := []llm.Message{{Role: llm.RoleSystem, Content: navigatorSystemPrompt}}
	tail := make([]llm.Message, 0, 4)
	for i := len(visible) - 1; i >= 0 && len(tail) < 4; i-- {
		m := visible[i]
		if m.Role != llm.RoleUser && m.Role != llm.RoleAssistant {
			continue
		}
		if strings.Contains(m.Content, "<task-notification>") || len(m.ToolCalls) > 0 {
			continue
		}
		m.Content = truncateForNavigator(m.Content)
		// Message is copied by value, but Parts is a slice. Clone it before
		// truncating so navigator preparation never rewrites conversation
		// history (and never invalidates append-only token accounting).
		m.Parts = append([]llm.ContentPart(nil), m.Parts...)
		for i := range m.Parts {
			m.Parts[i].Text = truncateForNavigator(m.Parts[i].Text)
		}
		tail = append(tail, m)
	}
	for i := len(tail) - 1; i >= 0; i-- {
		out = append(out, tail[i])
	}
	out = append(out, llm.Message{Role: llm.RoleUser, Content: prompt})
	return out
}

func truncateForNavigator(s string) string {
	const max = 500
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func parseNavigatorMode(s string) (RouteMode, bool) {
	s = strings.ToLower(s)
	// Strip common thinking wrappers if the model exposes reasoning text.
	if idx := strings.LastIndex(s, "{"); idx >= 0 {
		s = s[idx:]
	}
	switch {
	case strings.Contains(s, `"mode":"chat"`) || strings.Contains(s, `"mode": "chat"`):
		return RouteChatOnly, true
	case strings.Contains(s, `"mode":"advisor"`) || strings.Contains(s, `"mode": "advisor"`):
		return RouteAdvisor, true
	case strings.Contains(s, `"mode":"coordinator"`) || strings.Contains(s, `"mode": "coordinator"`):
		return RouteCoordinator, true
	case strings.Contains(s, `"mode":"clarify"`) || strings.Contains(s, `"mode": "clarify"`):
		return RouteClarify, true
	default:
		return "", false
	}
}

// persist calls the writer if one is configured. A failed write
// must not abort the run — but it is no longer swallowed silently:
// persistAppend (persist_health.go) keeps the first error sticky,
// counts failures, buffers the message for in-order retry on the
// next append, and surfaces a one-shot warning to the user.
func (l *Loop) persist(ctx context.Context, msg llm.Message) {
	if l.writer == nil {
		return
	}
	// session_persist accumulates across the step's AppendMessage
	// calls. It OVERLAPS other phases (persists happen inside the
	// step, some from worker goroutines), so statsEndStep keeps it
	// out of the next_turn_prepare remainder math.
	t := time.Now()
	l.persistAppend(ctx, msg)
	l.recordPhase(stats.PhaseSessionPersist, time.Since(t))
}

func (l *Loop) persistProjection(ctx context.Context) {
	w, ok := l.writer.(contextProjectionWriter)
	if !ok {
		return
	}
	h := &l.persistHealth
	h.mu.Lock()
	// Saving now would pair a projection snapshot with a transcript boundary
	// that is missing buffered messages. Delay and rebuild after recovery.
	if h.outage || len(h.pending) > 0 {
		h.projectionDirty = true
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()

	visible := l.VisibleMessages()
	// Base/system-prefix messages are rebuilt from current config when a
	// loop is resumed. Persist only the conversation body, otherwise Web
	// GUI would prepend a fresh system prompt to a stale duplicate.
	lead := 0
	for lead < len(visible) && visible[lead].Role == llm.RoleSystem {
		lead++
	}
	if err := w.SaveContextProjection(ctx, visible[lead:]); err != nil {
		h.mu.Lock()
		h.projectionDirty = true
		h.projectionOutage = true
		warn := h.noteFailureLocked("context_projection", err)
		h.mu.Unlock()
		l.persistNotify(warn)
		return
	}
	h.mu.Lock()
	recovered := h.projectionOutage
	h.projectionDirty = false
	h.projectionOutage = false
	if recovered && !h.outage {
		h.warned = false
	}
	h.mu.Unlock()
	if recovered {
		l.persistNotify("session context projection persistence recovered")
	}
}

// retryDirtyProjection must run on the loop goroutine: it rebuilds the latest
// visible context after append recovery. Persisting an old saved slice with a
// new MAX(seq) boundary could otherwise skip newer messages on resume.
func (l *Loop) retryDirtyProjection(ctx context.Context) {
	h := &l.persistHealth
	h.mu.Lock()
	ready := h.projectionDirty && !h.outage && len(h.pending) == 0
	h.mu.Unlock()
	if ready {
		l.persistProjection(ctx)
	}
}

// statsStartStep opens a new telemetry turn for a step (1-based)
// and resets the wall-phase accumulator. Nil recorder = no-op, so
// loops built without stats (tests, child workers) pay nothing.
func (l *Loop) statsStartStep(step int) {
	if l.stats == nil {
		return
	}
	l.stepPhaseWall = 0
	l.stepAuxWall = 0
	l.stats.StartStep(step)
}

// recordPhase forwards one phase measurement to the recorder.
// Safe from any goroutine (the recorder locks internally) and
// with a nil recorder.
func (l *Loop) recordPhase(name string, d time.Duration) {
	if l.stats == nil {
		return
	}
	l.stats.RecordPhase(name, d)
}

// recordWallPhase records a phase that is part of the step's
// DISJOINT wall-clock pipeline (context_prepare, request_encode,
// backend_wait, stream_total, tool_execution) and adds it to the
// accumulator statsEndStep subtracts from the step total. Must be
// called from the run goroutine only.
func (l *Loop) recordWallPhase(name string, d time.Duration) {
	if l.stats == nil {
		return
	}
	l.stepPhaseWall += d
	l.stats.RecordPhase(name, d)
}

// recordAuxWall records the wall time of a model-powered aux
// operation (draft, compact summary, reflection) as its own
// "model:<purpose>" phase and adds it to both accumulators, so
// the surrounding CLI phases (context_prepare, the
// next_turn_prepare remainder) exclude it. Run goroutine only.
func (l *Loop) recordAuxWall(purpose string, d time.Duration) {
	if l.stats == nil {
		return
	}
	l.stepAuxWall += d
	l.recordWallPhase("model:"+purpose, d)
}

// statsEndStep attributes the unmeasured remainder of the step to
// next_turn_prepare (history append, eviction, draft accounting,
// reflection, event plumbing) and closes the telemetry turn.
// Called on every step exit path.
func (l *Loop) statsEndStep(stepStart time.Time) {
	if l.stats == nil {
		return
	}
	rest := time.Since(stepStart) - l.stepPhaseWall
	if rest < 0 {
		rest = 0
	}
	l.stats.RecordPhase(stats.PhaseNextTurnPrepare, rest)
	l.stats.EndStep()
}

// toolResult is the internal envelope for a single tool execution.
type toolResult struct {
	followUps []llm.Message
	fatal     bool
	failed    bool
	err       error
}

// adaptiveReflectionProgress is reset for every Run. It deliberately uses
// only facts the CLI already has; deciding whether the answer is "good" is
// left to the reflector only after a concrete signal justifies that model
// call. Hashing the batch keeps large write_file arguments out of loop state.
type adaptiveReflectionProgress struct {
	lastBatch         [sha256.Size]byte
	haveBatch         bool
	repeatedBatches   int
	failedBatchStreak int
}

func (p *adaptiveReflectionProgress) observe(step, maxSteps int, calls []llm.ToolCall, failures int) string {
	fingerprint := toolCallBatchFingerprint(calls)
	if p.haveBatch && fingerprint == p.lastBatch {
		p.repeatedBatches++
	} else {
		p.lastBatch = fingerprint
		p.haveBatch = true
		p.repeatedBatches = 1
	}
	if failures > 0 {
		p.failedBatchStreak++
	} else {
		p.failedBatchStreak = 0
	}

	// Two consecutive bad batches means the model has already had one
	// ordinary opportunity to self-correct from the structured diagnostic.
	if p.failedBatchStreak >= 2 {
		return "repeated_tool_failure"
	}
	// Repeating the exact names and arguments is a stronger no-progress
	// signal than merely using the same tool on a different file/range.
	if p.repeatedBatches >= 2 {
		return "repeated_tool_batch"
	}
	// A reflection after the final model step cannot influence anything.
	// Fire when exactly one useful provider call remains instead.
	if maxSteps > 1 && step == maxSteps-1 {
		return "step_budget_low"
	}
	return ""
}

func (p *adaptiveReflectionProgress) reset() {
	p.haveBatch = false
	p.repeatedBatches = 0
	p.failedBatchStreak = 0
}

func toolCallBatchFingerprint(calls []llm.ToolCall) [sha256.Size]byte {
	h := sha256.New()
	for _, call := range calls {
		h.Write([]byte(call.Name))
		h.Write([]byte{0})
		h.Write([]byte(call.Arguments))
		h.Write([]byte{0xff})
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func (l *Loop) runReflection(ctx context.Context, step int, reason string, out chan<- Event) {
	if l.reflector == nil {
		return
	}
	reflStart := time.Now()
	txt, err := l.reflector.Reflect(llm.WithPurpose(ctx, llm.PurposeReflect), l.Messages)
	l.recordAuxWall(llm.PurposeReflect, time.Since(reflStart))
	if err != nil || strings.TrimSpace(txt) == "" {
		// Best-effort: reflection must never break the user's run.
		return
	}
	refMsg := llm.Message{
		Role:    llm.RoleSystem,
		Content: fmt.Sprintf("[reflection checkpoint @ step %d; reason=%s] %s", step, reason, txt),
	}
	l.Messages = append(l.Messages, refMsg)
	l.persist(ctx, refMsg)
	out <- ReflectionEvent{Step: step, Text: txt, Reason: reason}
}

// invoke runs a single tool call, emits the matching events, and
// returns the messages to append to history.
func (l *Loop) invoke(ctx context.Context, tc llm.ToolCall, out chan<- Event) toolResult {
	// Wave 1 hardening for small models: validate the tool name
	// (with a did-you-mean suggestion) and repair truncated or
	// unbalanced JSON arguments before execution. An unrepairable
	// call is bounced back to the model with the correct format so
	// it can retry (up to maxToolFormatRetries consecutive times).
	if errMsg := HardenToolCall(&tc, l.registry.Names(), l.recentBadCallStreak()); errMsg != "" {
		out <- ToolCallEvent{ID: tc.ID, Name: tc.Name, Args: tc.Arguments}
		out <- ToolResultEvent{ID: tc.ID, Err: fmt.Errorf("%s", errMsg)}
		return toolResult{
			failed: true,
			followUps: []llm.Message{{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    errMsg,
			}},
		}
	}

	raw := json.RawMessage(tc.Arguments)
	// Announce the call before execution. Long-running tools (most notably a
	// synchronous task worker) must become visible to TUI/WebGUI while they are
	// running, not only after Execute returns. ToolResultEvent remains the
	// matching completion edge, so clients can measure and render real elapsed
	// time without a separate worker-specific protocol.
	out <- ToolCallEvent{ID: tc.ID, Name: tc.Name, Args: tc.Arguments}

	// Per-tool timing under a "tool:<name>" phase key. Repeated calls
	// of the same tool in one step accumulate; the recorder is
	// mutex-guarded so parallel task batches are safe. Goes straight
	// to the recorder (NOT the wall-phase sum): the whole batch is
	// already covered by tool_execution in run().
	execStart := time.Now()
	var res tools.Result
	var err error
	if isPassingGoalVerification(tc.Name, raw) && (!l.toolEvidence.Load() || l.concreteFailure.Load()) {
		res.Err = fmt.Errorf("goal: passing verification requires a successful concrete check after the latest tool failure")
	} else if isGoalTaskCompletion(tc.Name, raw) && l.concreteFailure.Load() {
		res.Err = fmt.Errorf("goal: cannot complete a task after a failed tool result; fix the failure and run a successful concrete check first")
	} else {
		res, err = l.registry.Execute(ctx, tc.Name, raw)
	}
	l.recordPhase("tool:"+tc.Name, time.Since(execStart))

	// F4.e: tool result verification. After a successful
	// (no Go-level error) execution, ask the per-tool
	// override or the DefaultVerifier whether the result
	// is trustworthy. A failure rewrites res to surface
	// the reason instead of the (potentially lying)
	// tool output.
	if err == nil {
		tool, ok := l.registry.Get(tc.Name)
		if ok {
			res = tools.ApplyVerification(tools.Check{
				Tool:    tc.Name,
				Args:    raw,
				Result:  res,
				BaseDir: l.baseDir,
			}, tool.Verify)
		}
	}

	// F4.d: classify any error/verification failure and
	// log it to the error log if configured. We log
	// after the verification rewrite so the log records
	// the reason the model actually sees.
	if l.errorLog != nil {
		cat := tools.Classifier{}.Classify(tc.Name, raw, res)
		if cat.Category != tools.CategoryUnknown {
			l.errorLog.Append(tools.ErrorRecord{
				Tool:       tc.Name,
				Args:       string(raw),
				Category:   cat.Category.String(),
				Confidence: cat.Confidence,
				Reason:     cat.Reason,
				Suggestion: cat.Suggestion,
			})
		}
	}

	// Tool not found.
	if err != nil {
		if isConcreteEvidenceTool(tc.Name) {
			l.concreteFailure.Store(true)
		}
		out <- ToolResultEvent{ID: tc.ID, Err: err}
		return toolResult{
			failed: true,
			followUps: []llm.Message{{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    "error: " + err.Error(),
			}},
		}
	}
	if res.Err != nil {
		if isConcreteEvidenceTool(tc.Name) {
			l.concreteFailure.Store(true)
		}
		out <- ToolResultEvent{ID: tc.ID, Output: res.Text, Err: res.Err}
		return toolResult{
			failed: true,
			followUps: []llm.Message{{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				// ModelContent is the single contract point for
				// what the model sees: the error PLUS a capped
				// tail of res.Text (deduplicated), so diagnostics
				// a tool returns next to its error are not lost.
				Content: res.ModelContent(),
			}},
		}
	}
	if isConcreteEvidenceTool(tc.Name) {
		l.toolEvidence.Store(true)
		l.concreteFailure.Store(false)
	}

	out <- ToolResultEvent{ID: tc.ID, Output: res.Text}
	modelContent := l.registry.CompactModelOutput(tc.Name, res.ModelContent())
	follow := []llm.Message{{
		Role:       llm.RoleTool,
		ToolCallID: tc.ID,
		Name:       tc.Name,
		Content:    modelContent,
	}}
	if res.Image != nil {
		img := &llm.ImageRef{
			MediaType: res.Image.MediaType,
			Data:      base64.StdEncoding.EncodeToString(res.Image.Data),
		}
		if !l.visionOK() {
			follow[0].Content += "\n[note: image dropped, model lacks vision]"
		} else {
			follow = append(follow, llm.Message{
				Role: llm.RoleUser,
				Parts: []llm.ContentPart{
					{Type: llm.PartTypeText, Text: "Attached image from tool " + tc.Name + ":"},
					{Type: llm.PartTypeImage, Image: img},
				},
			})
		}
	}
	return toolResult{followUps: follow}
}

func isPassingGoalVerification(name string, raw json.RawMessage) bool {
	if name != "goal" {
		return false
	}
	var p struct {
		Action string `json:"action"`
		Passed bool   `json:"passed"`
	}
	return json.Unmarshal(raw, &p) == nil && p.Action == "verify" && p.Passed
}

func isGoalTaskCompletion(name string, raw json.RawMessage) bool {
	if name != "goal" {
		return false
	}
	var p struct {
		Action string `json:"action"`
	}
	return json.Unmarshal(raw, &p) == nil && p.Action == "complete_task"
}

func isConcreteEvidenceTool(name string) bool {
	switch name {
	case "", "goal", "tool_search", "recall", "remember", "ask_user", "send_message", "task_stop":
		return false
	default:
		return true
	}
}

// visionOK reports whether the configured model can see images.
func (l *Loop) visionOK() bool {
	if l.caps == nil {
		return true
	}
	return l.caps.HasVision(l.provider.Name())
}

// sisyphusHitFromMessage extracts the 1-indexed attempt
// counter from a Sisyphus reminder so the TUI can render
// "Sisyphus #1/3" etc. The reminder format is
// "[Sisyphus @N/M] ..."; we parse N out of the bracket.
// If parsing fails, return 1 so the UI never shows "0".
func sisyphusHitFromMessage(msg string) int {
	const tag = "[Sisyphus @"
	i := strings.Index(msg, tag)
	if i < 0 {
		return 1
	}
	rest := msg[i+len(tag):]
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return 1
	}
	n := 0
	for _, ch := range rest[:slash] {
		if ch < '0' || ch > '9' {
			return 1
		}
		n = n*10 + int(ch-'0')
	}
	if n <= 0 {
		return 1
	}
	return n
}

// toolCallScanner accumulates streamed text in a strings.Builder
// (amortized O(n) instead of the quadratic `text += delta`) and
// tracks marker positions incrementally so the full-buffer
// extractXMLToolCalls / extractSentinelToolCalls scans run only
// when a complete block can actually be present. Each appended
// delta is scanned once (plus a marker-length-1 overlap so a
// marker split across deltas is still found), never the whole
// buffer.
type toolCallScanner struct {
	buf strings.Builder
	// emitted is the byte prefix already surfaced as MessageEvents. Tool-call
	// markers are buffered until parsed, so raw sentinel/XML calls never flash
	// in streaming UIs and prose before a call is never emitted twice.
	emitted int

	xmlOpen   int  // index of first "<tool_call>", -1 if none
	xmlClose  bool // "</tool_call>" seen after xmlOpen
	xmlFailed bool // complete block parsed to zero calls (deterministic → skip)

	sentOpen   int // index of first «, -1 if none
	sentClose  bool
	sentFailed bool
}

func newToolCallScanner() *toolCallScanner {
	return &toolCallScanner{xmlOpen: -1, sentOpen: -1}
}

// append adds a delta and scans only the new tail for markers.
func (sc *toolCallScanner) append(delta string) {
	prev := sc.buf.Len()
	sc.buf.WriteString(delta)
	sc.scanFrom(prev)
}

// reset replaces the buffer with the text remaining after an
// extraction and recomputes marker state from scratch (the
// remainder is short: it is only the prose before the block).
func (sc *toolCallScanner) reset(remaining string) {
	sc.buf.Reset()
	sc.emitted = 0
	sc.xmlOpen, sc.xmlClose, sc.xmlFailed = -1, false, false
	sc.sentOpen, sc.sentClose, sc.sentFailed = -1, false, false
	sc.buf.WriteString(remaining)
	sc.scanFrom(0)
}

// safeEmitEnd returns the largest prefix known not to belong to a tool-call
// marker or body. A partial marker at the end is retained until the next delta
// (e.g. "<tool_c" + "all>" or a split UTF-8 guillemet).
func (sc *toolCallScanner) safeEmitEnd() int {
	s := sc.buf.String()
	end := len(s)
	if sc.xmlOpen >= 0 && !sc.xmlFailed && sc.xmlOpen < end {
		end = sc.xmlOpen
	}
	if sc.sentOpen >= 0 && !sc.sentFailed && sc.sentOpen < end {
		end = sc.sentOpen
	}
	if end < len(s) {
		return end
	}
	for _, marker := range []string{"<tool_call>", sentinelOpen} {
		max := len(marker) - 1
		if max > len(s) {
			max = len(s)
		}
		for n := max; n > 0; n-- {
			if strings.HasSuffix(s, marker[:n]) && len(s)-n < end {
				end = len(s) - n
				break
			}
		}
	}
	return end
}

// scanFrom updates marker state by scanning from prev (minus a
// marker-length-1 overlap for markers split across deltas).
func (sc *toolCallScanner) scanFrom(prev int) {
	const xmlOpenTag = "<tool_call>"
	const xmlCloseTag = "</tool_call>"
	s := sc.buf.String()

	scanOpen := func(open string, at *int) {
		if *at >= 0 {
			return
		}
		from := prev - len(open) + 1
		if from < 0 {
			from = 0
		}
		if i := strings.Index(s[from:], open); i >= 0 {
			*at = from + i
		}
	}
	scanClose := func(open, close string, openAt int, seen *bool) {
		if openAt < 0 || *seen {
			return
		}
		// A close marker only counts after the open marker; also
		// re-check the overlap window for split markers.
		from := prev - len(close) + 1
		if min := openAt + len(open); from < min {
			from = min
		}
		if from <= len(s) && strings.Contains(s[from:], close) {
			*seen = true
		}
	}

	scanOpen(xmlOpenTag, &sc.xmlOpen)
	scanClose(xmlOpenTag, xmlCloseTag, sc.xmlOpen, &sc.xmlClose)
	scanOpen(sentinelOpen, &sc.sentOpen)
	scanClose(sentinelOpen, sentinelClose, sc.sentOpen, &sc.sentClose)
}

// xmlReady/sentReady report whether the corresponding extract
// function could return a non-empty result for the buffer.
func (sc *toolCallScanner) xmlReady() bool {
	return sc.xmlOpen >= 0 && sc.xmlClose && !sc.xmlFailed
}
func (sc *toolCallScanner) sentReady() bool {
	return sc.sentOpen >= 0 && sc.sentClose && !sc.sentFailed
}

// consume drains the provider channel, emitting MessageEvents and
// collecting tool calls + usage.
// It also detects XML <tool_call> blocks as a fallback for models
// that don't support native function calling.
func (l *Loop) consume(ctx context.Context, stream <-chan llm.Delta, out chan<- Event) (string, []llm.ToolCall, *llm.Usage, error) {
	var toolCalls []llm.ToolCall
	var usage *llm.Usage
	sc := newToolCallScanner()
	emitTo := func(end int) error {
		if end <= sc.emitted {
			return nil
		}
		text := sc.buf.String()[sc.emitted:end]
		select {
		case out <- MessageEvent{Text: text}:
			sc.emitted = end
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// backend_wait (TTFT) is ONE timestamp taken at the first delta;
	// stream_total is one measurement at stream close. Nothing is
	// timed per-delta — the streaming hot path pays a single zero-
	// value comparison per delta and no allocations.
	waitStart := time.Now()
	var firstDelta time.Time
	defer func() {
		if firstDelta.IsZero() {
			// The stream ended (or errored) before any delta:
			// the whole wait was backend time.
			l.recordWallPhase(stats.PhaseBackendWait, time.Since(waitStart))
			return
		}
		l.recordWallPhase(stats.PhaseStreamTotal, time.Since(firstDelta))
	}()
	for d := range stream {
		if firstDelta.IsZero() {
			firstDelta = time.Now()
			l.recordWallPhase(stats.PhaseBackendWait, firstDelta.Sub(waitStart))
		}
		if err := ctx.Err(); err != nil {
			return sc.buf.String(), toolCalls, usage, err
		}
		if d.Err != nil {
			return sc.buf.String(), toolCalls, usage, d.Err
		}
		if d.Notice != "" {
			// Informational status (rate-limit retry etc.) — surface
			// to the UI, never into the conversation text.
			select {
			case out <- NoticeEvent{Text: d.Notice}:
			case <-ctx.Done():
				return sc.buf.String(), toolCalls, usage, ctx.Err()
			}
			continue
		}
		if d.Content != "" {
			sc.append(d.Content)

			// XML tool call fallback: detect <tool_call> blocks
			// in the accumulated text and convert to real tool calls.
			// Gated by the incremental scanner so the O(buffer) pass
			// runs only when a complete block is actually present.
			if sc.xmlReady() {
				tcs, remaining := extractXMLToolCalls(sc.buf.String())
				if len(tcs) > 0 {
					// Only emit the not-yet-streamed portion before the block.
					if err := emitTo(len(remaining)); err != nil {
						return sc.buf.String(), toolCalls, usage, err
					}
					toolCalls = append(toolCalls, tcs...)
					// Reset text to just the remaining portion.
					sc.reset(remaining)
					sc.emitted = len(remaining)
					continue
				}
				// The first complete block is fixed once seen and the
				// parse is deterministic — never retry it.
				sc.xmlFailed = true
			}

			// Sentinel tool call (thin protocol B3): detect «...»
			// blocks. Same streaming contract as the XML fallback —
			// checked after XML so the historical path is untouched.
			if sc.sentReady() {
				stcs, sbefore := extractSentinelToolCalls(sc.buf.String())
				if len(stcs) > 0 {
					if err := emitTo(len(sbefore)); err != nil {
						return sc.buf.String(), toolCalls, usage, err
					}
					toolCalls = append(toolCalls, stcs...)
					sc.reset(sbefore)
					sc.emitted = len(sbefore)
					continue
				}
				sc.sentFailed = true
			}

			if err := emitTo(sc.safeEmitEnd()); err != nil {
				return sc.buf.String(), toolCalls, usage, err
			}
		}
		if d.ToolCall != nil {
			toolCalls = append(toolCalls, *d.ToolCall)
		}
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	// No complete tool block claimed the retained suffix: surface it as plain
	// text (including malformed/incomplete markers) rather than losing output.
	if err := emitTo(sc.buf.Len()); err != nil {
		return sc.buf.String(), toolCalls, usage, err
	}
	return sc.buf.String(), toolCalls, usage, nil
}

// extractXMLToolCalls scans text for <tool_call>...</tool_call>
// blocks. Returns parsed tool calls and the text BEFORE the first
// XML block. If no complete block is found, returns nil, "".
func extractXMLToolCalls(text string) ([]llm.ToolCall, string) {
	const open = "<tool_call>"
	const close = "</tool_call>"

	start := strings.Index(text, open)
	if start < 0 {
		return nil, ""
	}
	end := strings.Index(text[start:], close)
	if end < 0 {
		return nil, "" // not yet complete (streaming)
	}
	end += start + len(close)

	// Text before the XML block.
	before := text[:start]
	xmlBlock := text[start:end]

	tcs := parseXMLToolCallBlock(xmlBlock)
	return tcs, before
}

// parseXMLToolCallBlock parses a single <tool_call>...</tool_call>
// block. Supports format:
//
//	<tool_call>
//	<function=NAME>
//	<parameter=KEY>VALUE</parameter>
//	</function>
//	</tool_call>
func parseXMLToolCallBlock(block string) []llm.ToolCall {
	block = strings.TrimSpace(block)

	// Find <function=NAME>...</function>
	funcStart := strings.Index(block, "<function=")
	if funcStart < 0 {
		return nil
	}
	funcEnd := strings.Index(block[funcStart:], "</function>")
	if funcEnd < 0 {
		// Try self-closing: <function=NAME/>
		funcEnd = strings.Index(block[funcStart:], "/>")
		if funcEnd < 0 {
			return nil
		}
		funcEnd += funcStart + 1 // point to the '/'
	} else {
		funcEnd += funcStart + len("</function>")
	}

	funcBlock := block[funcStart:funcEnd]
	name := extractXMLFuncName(funcBlock)
	if name == "" {
		return nil
	}

	// Build JSON args from <parameter=KEY>VALUE</parameter> pairs.
	// We collect names and values separately so we can also recognise
	// the single-blob variant some Hermes/Qwen models emit:
	// <parameter=arguments>{...json...}</parameter>, where the whole
	// argument object is packed into one "arguments" parameter rather
	// than one parameter per field.
	var pairs []string
	var names, values []string
	rem := funcBlock
	for {
		pi := strings.Index(rem, "<parameter=")
		if pi < 0 {
			break
		}
		rem = rem[pi+len("<parameter="):]
		// Find end of parameter name (before >).
		nameEnd := strings.IndexByte(rem, '>')
		if nameEnd < 0 {
			break
		}
		paramName := rem[:nameEnd]
		rem = rem[nameEnd+1:]

		// Find closing tag.
		closeTag := "</parameter>"
		ci := strings.Index(rem, closeTag)
		if ci < 0 {
			// Self-closing: <parameter=KEY/>
			ci = strings.Index(rem, "/>")
			if ci < 0 {
				break
			}
			pairs = append(pairs, fmt.Sprintf(`"%s":""`, paramName))
			names = append(names, paramName)
			values = append(values, "")
			rem = rem[ci+2:]
			continue
		}
		value := strings.TrimSpace(rem[:ci])
		rem = rem[ci+len(closeTag):]

		names = append(names, paramName)
		values = append(values, value)
		// Try to parse value as JSON; if it's not valid JSON,
		// treat it as a string.
		pairs = append(pairs, fmt.Sprintf(`"%s":%s`, paramName, jsonString(value)))
	}

	if len(pairs) == 0 {
		return nil
	}

	// Blob variant: exactly one parameter named "arguments" (or "args")
	// whose value is a JSON object. Use that object directly as the
	// arguments instead of nesting it under "arguments", which no tool
	// expects. Falls through to the per-field path on any mismatch.
	if len(names) == 1 && (names[0] == "arguments" || names[0] == "args") {
		if blob := strings.TrimSpace(values[0]); len(blob) > 1 &&
			blob[0] == '{' && blob[len(blob)-1] == '}' &&
			json.Valid([]byte(blob)) {
			return []llm.ToolCall{{
				ID:        "xml_" + name,
				Name:      name,
				Arguments: blob,
			}}
		}
	}

	args := "{" + strings.Join(pairs, ",") + "}"
	return []llm.ToolCall{{
		ID:        "xml_" + name,
		Name:      name,
		Arguments: args,
	}}
}

func extractXMLFuncName(funcBlock string) string {
	// <function=NAME> or <function=NAME/>
	start := strings.Index(funcBlock, "<function=")
	if start < 0 {
		return ""
	}
	rem := funcBlock[start+len("<function="):]
	end := strings.IndexAny(rem, ">/")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rem[:end])
}

// jsonString wraps a value as a JSON string, handling
// edge cases. If the value already looks like valid JSON
// (starts with { or [), return it as-is (object/array).
func jsonString(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return `""`
	}
	if (v[0] == '{' || v[0] == '[') && (v[len(v)-1] == '}' || v[len(v)-1] == ']') {
		return v // already JSON object/array
	}
	// Escape double quotes and wrap.
	escaped := strings.ReplaceAll(v, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

// invokeDraft asks the draft bridge for a plan and
// injects it as a system message. Records the draft
// output tokens for later savings computation. A
// failure (empty plan, context cancel, provider
// error) is logged via silent no-op — drafts are
// best-effort and must never abort the verifier
// run.
func (l *Loop) invokeDraft(ctx context.Context, step int, _ chan<- Event) {
	if l.draftBridge == nil {
		return
	}
	// Build the draft input: the most-recent user
	// message PLUS a trimmed slice of recent
	// conversation (last few messages, tool outputs
	// truncated hard) so the draft model is actually
	// informed about what already happened, while
	// keeping token cost low — cheap drafts are the
	// whole point of F11.
	prompt := l.draftPrompt()
	if prompt == "" {
		return
	}
	draftStart := time.Now()
	res, err := l.draftBridge.Plan(llm.WithPurpose(ctx, llm.PurposeDraft), prompt)
	l.recordAuxWall(llm.PurposeDraft, time.Since(draftStart))
	if err != nil || strings.TrimSpace(res.Text) == "" {
		// Silent no-op: a draft failure must not
		// break the run. Reset state so the
		// post-verifier outcome step sees a clean
		// slate.
		l.lastDraftText = ""
		l.lastDraftTokens = 0
		return
	}
	sysMsg := l.draftBridge.AsSystemMessage(res.Text)
	l.Messages = append(l.Messages, sysMsg)
	l.persist(ctx, sysMsg)
	l.lastDraftText = res.Text
	l.lastDraftTokens = res.Tokens
	l.draftPolicy.MarkDrafted(step)
}

// lastUserPrompt returns the most recent user-role
// message content. Used by the draft bridge as the
// "what to plan for" input.
func (l *Loop) lastUserPrompt() string {
	for i := len(l.Messages) - 1; i >= 0; i-- {
		if l.Messages[i].Role == llm.RoleUser {
			return l.Messages[i].Content
		}
	}
	return ""
}

// Draft-context trimming bounds. The draft model gets the
// last draftContextMessages messages of conversation; tool
// results are truncated to draftToolOutputCap characters and
// other messages to draftMessageCap. Cheap and informed beats
// cheap and blind.
const (
	draftContextMessages = 6
	draftToolOutputCap   = 300
	draftMessageCap      = 600
)

// draftPrompt builds the input for the draft model: a trimmed
// view of the recent conversation followed by the current user
// request. Returns "" when there is no user message at all
// (nothing to plan for).
func (l *Loop) draftPrompt() string {
	userPrompt := l.lastUserPrompt()
	if userPrompt == "" {
		return ""
	}
	// Collect the last N messages (excluding system
	// messages — draft plans and reflections would only
	// add noise) in chronological order.
	var recent []string
	count := 0
	for i := len(l.Messages) - 1; i >= 0 && count < draftContextMessages; i-- {
		m := l.Messages[i]
		if m.Role == llm.RoleSystem {
			continue
		}
		text := messageDraftText(m)
		if text == "" {
			continue
		}
		limit := draftMessageCap
		label := string(m.Role)
		if m.Role == llm.RoleTool {
			limit = draftToolOutputCap
			label = "tool result"
		}
		if len(text) > limit {
			text = text[:limit] + " ...[truncated]"
		}
		recent = append([]string{label + ": " + text}, recent...)
		count++
	}
	if len(recent) <= 1 {
		// Nothing beyond the user prompt itself: keep
		// the old cheap behavior.
		return userPrompt
	}
	var sb strings.Builder
	sb.WriteString("Recent conversation (oldest first, tool outputs truncated):\n")
	for _, r := range recent {
		sb.WriteString(r)
		sb.WriteString("\n")
	}
	sb.WriteString("\nCurrent request: ")
	sb.WriteString(userPrompt)
	return sb.String()
}

// messageDraftText extracts a plain-text rendering of a
// message for the draft context: Content, text parts, and
// tool-call names (so the draft sees WHICH tools ran even
// when the assistant message had no prose).
func messageDraftText(m llm.Message) string {
	var sb strings.Builder
	if m.Content != "" {
		sb.WriteString(m.Content)
	}
	for _, p := range m.Parts {
		if p.Type == llm.PartTypeText && p.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(p.Text)
		}
	}
	if len(m.ToolCalls) > 0 {
		var names []string
		for _, tc := range m.ToolCalls {
			names = append(names, tc.Name)
		}
		if sb.Len() > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString("[called tools: " + strings.Join(names, ", ") + "]")
	}
	return strings.TrimSpace(sb.String())
}

// recordDraftUsage charges the draft call's token
// usage to the credit tracker. Draft spend shares
// the F7 budget (D2 decision). Called once per draft
// turn, after the verifier's usage is recorded.
func (l *Loop) recordDraftUsage(ctx context.Context) error {
	if l.creditTracker == nil || l.draftBridge == nil {
		return nil
	}
	// Draft providers report input tokens too
	// (the system + user prompt). For now we
	// pass output as 0/0 because the bridge
	// captured only output tokens; the tracker
	// charges the verifier's total. We add a
	// second Record call with the draft's
	// (input, output) so the ledger shows the
	// draft spend. The total cost is the sum
	// of both records.
	draftModel := l.draftBridge.ModelName()
	// We don't have input tokens separately;
	// charge the output tokens as out. This
	// under-counts the draft's true cost
	// (input tokens) but keeps the ledger
	// honest about what we know. A future
	// enhancement would thread usage through
	// invokeDraft and store both halves.
	return l.creditTracker.Record(ctx, 0, int64(l.lastDraftTokens), draftModel)
}

// recordDraftOutcome computes the F11 savings /
// override signal. Emits DraftUsedEvent. Records
// savings to stats. If the verifier overrode the
// draft, writes a record to the override sink so
// the F5 reflector can learn from it.
func (l *Loop) recordDraftOutcome(step int, verifierText string, verifyTokens int, out chan<- Event) {
	if l.draftBridge == nil {
		return
	}
	draftText := l.lastDraftText
	draftTokens := l.lastDraftTokens
	savings, decision := l.draftSavings.Add(draftText, verifierText, draftTokens, verifyTokens)

	// Emit the event the TUI renders.
	out <- DraftUsedEvent{
		Step:          step,
		DraftModel:    l.draftBridge.ModelName(),
		VerifierModel: l.modelID,
		Decision:      decision,
		Savings:       savings,
	}

	// Feed the stats recorder.
	if l.stats != nil {
		l.stats.RecordSaved(savings)
	}

	// If the verifier overrode, record the pair
	// so the F5 reflector can learn from it.
	// Override detection: savings == 0 AND
	// decision == "overridden" (set by Savings.Add).
	if decision == "overridden" && l.draftOverrideSink != nil {
		_ = l.draftOverrideSink.RecordDraftOverride(ctxForOutcome(l), DraftOverride{
			Step:          step,
			DraftModel:    l.draftBridge.ModelName(),
			VerifierModel: l.modelID,
			DraftText:     draftText,
			VerifierText:  verifierText,
		})
	}

	// Clear last-draft state so the next
	// iteration (which may or may not have a
	// draft) starts fresh.
	l.lastDraftText = ""
	l.lastDraftTokens = 0
}

// ctxForOutcome returns a fresh background context
// for the override sink write. We deliberately
// don't pass the run's ctx because the sink is
// best-effort and must not be cancelled by user
// ctrl-c. Returns context.Background via a small
// helper to keep the call site tidy.
func ctxForOutcome(l *Loop) context.Context {
	return context.Background()
}

// SetExternalSink registers a channel that the
// loop can use to surface events that are NOT
// tied to a single Run — for example, F12
// ConsultEvent markers triggered by the /council
// slash command or the consult tool while the
// model is mid-turn. The channel is owned by the
// caller (typically the TUI's external-event
// pump); the loop never closes it. nil clears
// the sink. The loop holds no lock on the field
// — SetExternalSink must be called before any
// goroutine that may Emit.
func (l *Loop) SetExternalSink(ch chan<- Event) {
	l.extOut = ch
}

// Emit sends an event to the external sink if
// one is set. Non-blocking: when no sink is
// registered, when the sink is full, or when the
// caller is racing with a SetExternalSink(nil),
// the event is silently dropped. This matches
// the F2 design: external events are
// best-effort markers; losing one is preferable
// to blocking the tool layer.
//
// Returns true when the event was accepted, false
// otherwise. Currently informational; the only
// caller (the consult tool's OnResult) ignores it.
func (l *Loop) Emit(ev Event) bool {
	if l.extOut == nil {
		return false
	}
	select {
	case l.extOut <- ev:
		return true
	default:
		return false
	}
}

// InjectUserMessage appends an out-of-band user-role message to the loop. It is
// used by background workers to deliver task notifications to the coordinator's
// future context without requiring the user to paste them manually.
func (l *Loop) InjectUserMessage(ctx context.Context, content string) {
	if l == nil || strings.TrimSpace(content) == "" {
		return
	}
	msg := llm.Message{Role: llm.RoleUser, Content: content}
	l.Messages = append(l.Messages, msg)
	l.persist(ctx, msg)
}

// SetNextUserAddon queues text that will be appended once to the
// NEXT Run's user message, then cleared. It must be called before
// that Run starts (same goroutine discipline as SetExternalSink).
// The addon lands on the variable side of the prompt (a user
// message), never in the system prefix — KV-cache-prefix safe.
func (l *Loop) SetNextUserAddon(s string) {
	l.nextUserAddon = strings.TrimSpace(s)
}

// SetNextCoordinatorAddon queues text for the next coordinator-routed Run.
// Chat/advisor turns skip it without consuming it. This is the preferred API
// for automatically collected repository context.
func (l *Loop) SetNextCoordinatorAddon(s string) {
	l.nextCoordinatorAddon = strings.TrimSpace(s)
}

const maxPendingInterjections = 8

// QueueInterjection accepts a user message while Run is active. It performs no
// model call and never mutates Messages from the caller goroutine; run() drains
// it at the next assistant/tool boundary. False means empty input or a full
// queue, allowing the UI to keep the draft for retry.
func (l *Loop) QueueInterjection(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	l.interjectionMu.Lock()
	defer l.interjectionMu.Unlock()
	if len(l.interjections) >= maxPendingInterjections {
		return false
	}
	l.interjections = append(l.interjections, s)
	return true
}

func (l *Loop) drainInterjections(ctx context.Context) int {
	l.interjectionMu.Lock()
	pending := append([]string(nil), l.interjections...)
	l.interjections = l.interjections[:0]
	l.interjectionMu.Unlock()
	for _, text := range pending {
		msg := llm.Message{Role: llm.RoleUser, Content: text}
		l.Messages = append(l.Messages, msg)
		l.persist(ctx, msg)
	}
	if len(pending) > 0 {
		l.invalidateVisibleEstimate()
	}
	return len(pending)
}

// CurrentModel returns the name of the active provider.
func (l *Loop) CurrentModel() string {
	return l.modelID
}

// SetModel swaps the provider and model ID at runtime.
// Used by /model hot-swap (F26.5). The capability
// check is the caller's responsibility.
func (l *Loop) SetModel(p llm.Provider) {
	l.provider = p
	l.modelID = p.Name()
}

// ListModels returns all models from the capability registry.
func (l *Loop) ListModels() []llm.ModelInfo {
	if l.caps == nil {
		return nil
	}
	return l.caps.All()
}
