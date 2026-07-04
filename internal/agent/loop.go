package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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

// SessionReader is the resume contract. F2.c reads the
// conversation history back as []llm.Message.
type SessionReader interface {
	ReadMessages(ctx context.Context) ([]llm.Message, error)
}

// Loop is the production Agent. It streams model output, executes
// tool calls, feeds the results back, and repeats until the model
// emits a "stop" finish reason or MaxSteps is hit.
type Loop struct {
	provider        llm.Provider
	registry        *tools.Registry
	caps            *llm.CapabilityRegistry
	system          string
	briefing        string
	maxSteps        int
	thinTools       bool
	stableToolset   bool
	orchestrator    bool
	thinHintMax     int
	baseDir         string
	writer          SessionWriter
	errorLog        ErrorLogger
	reflector       Reflector
	reflectEvery    int
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

	// Messages is the running conversation. The loop appends to
	// it on every turn so the model sees the full history.
	Messages []llm.Message
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
	// Orchestrator enables the HARD delegation mode. It does not by
	// itself restrict the registry (the app layer passes a restricted
	// registry via OrchestratorRegistry); it only tells the loop to
	// treat `task` as a schema-carrying thin-core tool so delegation is
	// directly callable with a full schema from turn 1. Default false.
	Orchestrator bool
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
	// Reflector, when non-nil, is invoked every ReflectEvery
	// steps. The returned text is appended to Messages as a
	// system message BEFORE the next provider call. F5.a.
	Reflector Reflector
	// ReflectEvery controls how often reflection fires. 0
	// (or negative) disables reflection. Default 5 when
	// Reflector != nil.
	ReflectEvery int
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
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = 10
	}
	msgs := make([]llm.Message, 0, len(cfg.InitialMessages)+4)
	if cfg.System != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: cfg.System})
	}
	msgs = append(msgs, cfg.InitialMessages...)

	loop := &Loop{
		provider:        cfg.Provider,
		registry:        cfg.Registry,
		caps:            cfg.Caps,
		system:          cfg.System,
		briefing:        cfg.Briefing,
		maxSteps:        cfg.MaxSteps,
		thinTools:       cfg.ThinTools,
		stableToolset:   cfg.StableToolset,
		orchestrator:    cfg.Orchestrator,
		thinHintMax:     cfg.ThinHintMax,
		baseDir:         cfg.BaseDir,
		writer:          cfg.Writer,
		errorLog:        cfg.ErrorLog,
		reflector:       cfg.Reflector,
		reflectEvery:    cfg.ReflectEvery,
		patternInjector: cfg.PatternInjector,
		creditTracker:   cfg.CreditTracker,
		modelID:         cfg.Provider.Name(),
		windowFor:       cfg.WindowFor,
		summarizer:      cfg.Summarizer,
		learnLimit:      cfg.LearnLimit,
		Messages:        msgs,
		routeMap:        DefaultRouteMap(),
		route:           RouteCoordinator,
		navigate:        cfg.EnableNavigator,
		navAuto:         cfg.NavigatorAuto,
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
			loop.stats = cfg.Stats
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
func (l *Loop) SetRegistry(r *tools.Registry) { l.registry = r }

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
	if len(tail) > 0 {
		if body := tools.RenderCatalog(tail, l.thinHintMaxOrDefault()); body != "" {
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
	// F14: clear any hidden flags from a previous Run. The
	// user's /clear or hide_messages calls in this Run start
	// from a clean slate.
	l.resetHidden()
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
	l.Messages = append(l.Messages, userMsg)
	l.persist(ctx, userMsg)
	go l.run(ctx, prompt, out)
	return out, nil
}

func (l *Loop) run(ctx context.Context, prompt string, out chan<- Event) {
	defer close(out)
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
		} else {
			l.route = l.navigateRoute(ctx, prompt)
		}
	default:
		l.route = l.navigateRoute(ctx, prompt)
	}
	totalUsage := Usage{}
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

		// Wave 4: auto-compact before the provider call when
		// the visible token estimate crosses 80% of the
		// model's context window.
		l.maybeAutoCompact(ctx, out, "")

		// Build tool definitions from visible tools. Non-coordinator routes
		// get only the minimal chatRouteTools set (tool_search + recall) so
		// the model can pull in more when needed; the full tool list is the
		// actual token cost the router avoids.
		toolDefs := l.buildToolDefs()

		text, toolCalls, usage, err := l.completeOnce(ctx, toolDefs, out)
		if err != nil && l.handleContextOverflow(ctx, err, out) {
			// Wave 4: provider rejected the context size.
			// The learned limit was persisted and the
			// conversation compacted; retry once.
			text, toolCalls, usage, err = l.completeOnce(ctx, toolDefs, out)
		}
		if err != nil {
			out <- ErrorEvent{Err: err}
			return
		}
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
			l.lastTurnReasoning = usage.Reasoning
			l.lastTurnSet = true
			l.sessUsageMu.Unlock()
			// Report per-turn usage to the writer (if any).
			if l.writer != nil {
				_ = l.writer.UpdateUsage(usage.Input, usage.Output)
			}
			// F7: record to credit tracker. A budget
			// cap is a hard stop, but we still emit
			// the partial usage for the turn.
			if l.creditTracker != nil {
				if err := l.creditTracker.Record(ctx, int64(usage.Input), int64(usage.Output), l.modelID); err != nil {
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

		if len(toolCalls) == 0 {
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
					continue
				}
			}
			out <- DoneEvent{Usage: totalUsage}
			return
		}

		if !l.invokeToolCalls(ctx, toolCalls, out) {
			return
		}

		// F5.a: reflection checkpoint. After every step,
		// check if this is a reflection step and inject the
		// reflection as a mid-conversation system message.
		// The next iteration of the loop will see it as
		// part of the history.
		if l.reflector != nil && l.reflectEvery > 0 && (step+1)%l.reflectEvery == 0 {
			txt, err := l.reflector.Reflect(ctx, l.Messages)
			if err == nil && strings.TrimSpace(txt) != "" {
				refMsg := llm.Message{
					Role:    llm.RoleSystem,
					Content: fmt.Sprintf("[reflection checkpoint @ step %d] %s", step+1, txt),
				}
				l.Messages = append(l.Messages, refMsg)
				l.persist(ctx, refMsg)
				out <- ReflectionEvent{Step: step + 1, Text: txt}
			}
			// On error or empty body: silently skip;
			// reflection is best-effort, must not break
			// the run.
		}
	}
	out <- ErrorEvent{Err: fmt.Errorf("agent: max steps (%d) reached", l.maxSteps)}
}

// invokeToolCalls runs the model's tool-call batch and appends the matching
// tool-result messages to history. Most tools are executed sequentially to
// avoid surprising write conflicts. A batch made only of the coordinator's
// `task` calls is safe and useful to run concurrently: each task owns an
// isolated child loop/context, and parallel research workers are the main
// reason the coordinator mode exists.
func (l *Loop) invokeToolCalls(ctx context.Context, toolCalls []llm.ToolCall, out chan<- Event) bool {
	if len(toolCalls) > 1 && allTaskCalls(toolCalls) {
		return l.invokeTaskCallsParallel(ctx, toolCalls, out)
	}

	for _, tc := range toolCalls {
		ev := l.invoke(ctx, tc, out)
		for _, m := range ev.followUps {
			l.Messages = append(l.Messages, m)
			l.persist(ctx, m)
		}
		if ev.fatal {
			out <- ErrorEvent{Err: ev.err}
			return false
		}
	}
	return true
}

func allTaskCalls(toolCalls []llm.ToolCall) bool {
	for _, tc := range toolCalls {
		if tc.Name != "task" {
			return false
		}
	}
	return len(toolCalls) > 0
}

func (l *Loop) invokeTaskCallsParallel(ctx context.Context, toolCalls []llm.ToolCall, out chan<- Event) bool {
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
	for _, ev := range results {
		for _, m := range ev.followUps {
			l.Messages = append(l.Messages, m)
			l.persist(ctx, m)
		}
		if ev.fatal {
			out <- ErrorEvent{Err: ev.err}
			return false
		}
	}
	return true
}

// completeOnce performs one provider Complete call and
// consumes the stream. Split out so the run loop can retry
// once after a context-overflow compaction.
func (l *Loop) completeOnce(ctx context.Context, toolDefs []llm.ToolDef, out chan<- Event) (string, []llm.ToolCall, *llm.Usage, error) {
	stream, err := l.provider.Complete(ctx, l.providerMessages(), toolDefs)
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
		out = append(out, visible...)
		// Thin tool protocol: inject the sentinel call-format
		// instruction (and the dormant-tail catalog when non-empty)
		// just before the freshness stamp. Empty when thin tools are
		// off, so nothing is injected then.
		if pre := l.thinToolsPreamble(); pre != "" {
			out = append(out, llm.Message{Role: llm.RoleSystem, Content: pre})
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
	system += "\n\n" + l.stampSection()
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
	// Keep a tiny conversational tail before the current turn. Skip
	// system/tool messages and task notifications so background agent
	// work does not leak into smalltalk.
	end := len(visible)
	if lastUser >= 0 {
		end = lastUser
	}
	tail := make([]llm.Message, 0, 8)
	for i := end - 1; i >= 0 && len(tail) < 8; i-- {
		m := visible[i]
		if m.Role != llm.RoleUser && m.Role != llm.RoleAssistant {
			continue
		}
		if strings.Contains(m.Content, "<task-notification>") || len(m.ToolCalls) > 0 {
			continue
		}
		tail = append(tail, m)
	}
	for i := len(tail) - 1; i >= 0; i-- {
		out = append(out, tail[i])
	}
	if lastUser >= 0 {
		for _, m := range visible[lastUser:] {
			if m.Role == llm.RoleSystem {
				continue
			}
			out = append(out, m)
		}
	}
	return out
}

func (l *Loop) navigateRoute(ctx context.Context, prompt string) RouteMode {
	fallback := l.routeMap.Classify(prompt)
	msgs := l.navigatorMessages(prompt)
	stream, err := l.provider.Complete(ctx, msgs, nil)
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

// persist calls the writer if one is configured. Errors are
// intentionally swallowed: a failed write must not abort the
// run, only the persistence. F2.c logs the error to stderr; the
// F5 reflect module will surface it to the user.
func (l *Loop) persist(ctx context.Context, msg llm.Message) {
	if l.writer == nil {
		return
	}
	if err := l.writer.AppendMessage(ctx, msg); err != nil {
		// Best-effort. We could add a logger field later.
		_ = err
	}
}

// toolResult is the internal envelope for a single tool execution.
type toolResult struct {
	followUps []llm.Message
	fatal     bool
	err       error
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
			followUps: []llm.Message{{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    errMsg,
			}},
		}
	}

	raw := json.RawMessage(tc.Arguments)
	res, err := l.registry.Execute(ctx, tc.Name, raw)

	out <- ToolCallEvent{ID: tc.ID, Name: tc.Name, Args: tc.Arguments}

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
		out <- ToolResultEvent{ID: tc.ID, Err: err}
		return toolResult{
			followUps: []llm.Message{{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    "error: " + err.Error(),
			}},
		}
	}
	if res.Err != nil {
		out <- ToolResultEvent{ID: tc.ID, Output: res.Text, Err: res.Err}
		return toolResult{
			followUps: []llm.Message{{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    "error: " + res.Err.Error(),
			}},
		}
	}

	out <- ToolResultEvent{ID: tc.ID, Output: res.Text}
	follow := []llm.Message{{
		Role:       llm.RoleTool,
		ToolCallID: tc.ID,
		Name:       tc.Name,
		Content:    res.Text,
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

// consume drains the provider channel, emitting MessageEvents and
// collecting tool calls + usage.
// It also detects XML <tool_call> blocks as a fallback for models
// that don't support native function calling.
func (l *Loop) consume(ctx context.Context, stream <-chan llm.Delta, out chan<- Event) (text string, toolCalls []llm.ToolCall, usage *llm.Usage, err error) {
	var xmlBuf strings.Builder // buffers text between XML tool call markers
	for d := range stream {
		if err := ctx.Err(); err != nil {
			return text, toolCalls, usage, err
		}
		if d.Err != nil {
			return text, toolCalls, usage, d.Err
		}
		if d.Notice != "" {
			// Informational status (rate-limit retry etc.) — surface
			// to the UI, never into the conversation text.
			select {
			case out <- NoticeEvent{Text: d.Notice}:
			case <-ctx.Done():
				return text, toolCalls, usage, ctx.Err()
			}
			continue
		}
		if d.Content != "" {
			text += d.Content

			// XML tool call fallback: detect <tool_call> blocks
			// in the accumulated text and convert to real tool calls.
			tcs, remaining := extractXMLToolCalls(text)
			if len(tcs) > 0 {
				// Emit remaining text BEFORE the XML block.
				if remaining != "" {
					select {
					case out <- MessageEvent{Text: remaining}:
					case <-ctx.Done():
						return text, toolCalls, usage, ctx.Err()
					}
				}
				for _, tc := range tcs {
					toolCalls = append(toolCalls, tc)
				}
				// Reset text to just the remaining portion.
				text = remaining
				_ = xmlBuf
				continue
			}

			// Sentinel tool call (thin protocol B3): detect «...»
			// blocks. Same streaming contract as the XML fallback —
			// checked after XML so the historical path is untouched.
			stcs, sbefore := extractSentinelToolCalls(text)
			if len(stcs) > 0 {
				if sbefore != "" {
					select {
					case out <- MessageEvent{Text: sbefore}:
					case <-ctx.Done():
						return text, toolCalls, usage, ctx.Err()
					}
				}
				toolCalls = append(toolCalls, stcs...)
				text = sbefore
				continue
			}

			select {
			case out <- MessageEvent{Text: d.Content}:
			case <-ctx.Done():
				return text, toolCalls, usage, ctx.Err()
			}
		}
		if d.ToolCall != nil {
			toolCalls = append(toolCalls, *d.ToolCall)
		}
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	return text, toolCalls, usage, nil
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
	res, err := l.draftBridge.Plan(ctx, prompt)
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
