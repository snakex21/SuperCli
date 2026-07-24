// Package agent defines the core agent loop contract. The real
// implementation arrives in F1; for now the package exposes the
// types every other module already depends on (Name, Run, Event).
package agent

import (
	"context"
	"fmt"
	"time"
)

// Event is a single tick produced by the agent loop. The full union
// of events (message, tool call, tool result, done, error) is defined
// here so callers can switch on a closed type.
type Event interface{ event() }

// MessageEvent is a chunk of assistant text. Multiple MessageEvents
// may be emitted for a single assistant turn as the model streams.
type MessageEvent struct{ Text string }

func (MessageEvent) event() {}

// ToolCallEvent signals that the model wants to invoke a tool.
type ToolCallEvent struct {
	Name string
	Args string // raw JSON, decoded by the tool layer
	ID   string
}

func (ToolCallEvent) event() {}

// ToolResultEvent reports the outcome of a tool call back to the
// caller. The loop itself produces one of these per ToolCallEvent.
type ToolResultEvent struct {
	ID     string
	Output string
	Err    error
}

func (ToolResultEvent) event() {}

// DoneEvent is the terminal event of a successful run. Usage is the
// total token usage reported by the model. Steps is the number of model
// turns consumed by this Run.
type DoneEvent struct {
	Usage Usage
	Steps int
}

func (DoneEvent) event() {}

// ReflectionEvent is emitted by the F5.a loop when a self-
// reflection checkpoint fires. TUI may render it as a marker
// line; the model itself sees the reflection text injected
// as a system message in the conversation history.
type ReflectionEvent struct {
	Step   int
	Text   string
	Reason string // fixed_interval, repeated_tool_failure/batch, step_budget_low
}

func (ReflectionEvent) event() {}

// SisyphusEvent is emitted by the F9 ultrawork Sisyphus
// enforcer when it intercepts a "no more tool calls" turn
// and re-prompts the model to continue working on the
// active /goal's unfinished tasks. The Text is the
// reminder that is also appended to Messages as a system
// message, so the model and the TUI see the same thing.
//
// Hit is 1-indexed (first re-prompt is Hit=1).
type SisyphusEvent struct {
	Step int
	Hit  int
	Text string
}

func (SisyphusEvent) event() {}

// DraftUsedEvent is emitted by the F11 draft-model bridge
// when a draft model's plan was injected into the
// verifier's view. The TUI renders it as a discrete
// marker so the user can see the system is working and
// how many tokens the draft saved the verifier from
// producing.
//
// Step is the loop step (0-indexed) at which the draft
// was used. DraftModel and VerifierModel are the model
// ids the bridge used (e.g. "claude-haiku-4-5" and
// "claude-3-5-sonnet-latest"). Savings is the number of
// tokens the draft "paid for" the verifier — heuristic
// in V1, see internal/draft/savings.go for the exact
// formula. Decision describes how the verifier treated
// the draft: "injected" (draft went in, outcome unknown
// at emit time), "used" (verifier echoed/relied on
// draft), or "overridden" (verifier generated its own
// plan, draft was ignored).
type DraftUsedEvent struct {
	Step          int
	DraftModel    string
	VerifierModel string
	Decision      string
	Savings       int
}

func (DraftUsedEvent) event() {}

// MessagesHiddenEvent is emitted by the F14 selective
// context-deletion path whenever one or more messages are
// hidden from the model's view (auto budget eviction,
// manual hide_messages tool call, or the /clear slash
// command). The TUI surfaces it as a compact marker so the
// user knows context was trimmed. Hidden messages are
// still in l.Messages, still persisted, and still
// searchable via F13 search_history.
//
// Count is the number of messages collapsed into a
// "[earlier context cleared — N message(s) compacted]"
// placeholder. Reason is "budget", "manual", or "clear".
type MessagesHiddenEvent struct {
	Count  int
	Reason string
}

func (MessagesHiddenEvent) event() {}

// ConsultEvent is emitted by the F12 cross-model
// consultation path (the consult tool or the
// /council slash command) after a council run
// completes. The TUI surfaces it as a compact
// marker; the full candidate text is NOT
// included here — the tool result is what the
// model sees, and the TUI transcript is what the
// user sees.
//
// Question is the original question (truncated
// to 60 chars for marker readability). CandidateCount
// is the number of successful samples (post-filter).
// WinnerProvider is the name of the chosen
// candidate's provider; WinnerIndex is its
// 0-based position in the filtered slice.
// Reason is the judge's one-sentence pick
// rationale. AllFailed is true when every
// sample errored (no judge was called).
type ConsultEvent struct {
	Question       string
	CandidateCount int
	WinnerIndex    int
	WinnerProvider string
	Reason         string
	AllFailed      bool
	TotalTokens    int64
}

func (ConsultEvent) event() {}

// WorkerNotificationEvent is emitted when a background task worker finishes.
// The same XML payload is also injected into the parent loop's message history
// so the coordinator can see it on a later turn.
type WorkerNotificationEvent struct {
	TaskID  string
	Agent   string
	Status  string
	Summary string
	Text    string
}

func (WorkerNotificationEvent) event() {}

// WorkerProgressEvent surfaces factual activity from a delegated worker while
// its parent task tool is still running. It deliberately carries tool events,
// not private chain-of-thought. Output/arguments are capped at the producer so
// a verbose child cannot flood the parent UI stream.
type WorkerProgressEvent struct {
	TaskID string
	Agent  string
	Kind   string // tool_call or tool_result
	CallID string
	Tool   string
	Args   string
	Output string
	Err    string
}

func (WorkerProgressEvent) event() {}

// DraftOverrideSink is the F11 contract for recording
// "the verifier overrode the draft" instances so the
// F5 reflector can later learn when drafts help and
// when they hurt. The agent loop calls this once per
// override; the reflector package provides a default
// implementation that persists to a JSONL file. Nil
// sink = overrides are silently dropped (the test
// path uses this).
type DraftOverrideSink interface {
	RecordDraftOverride(ctx context.Context, ev DraftOverride) error
}

// DraftOverride is the (draft, verifier) pair the
// F5 reflector learns from. We capture the model ids,
// the user's prompt step, the draft's plan text, and
// the verifier's first text response so the reflector
// can later compute overlap and decide whether the
// pattern is "draft was useful" or "draft was noise".
type DraftOverride struct {
	Step          int
	DraftModel    string
	VerifierModel string
	DraftText     string
	VerifierText  string
	UserPrompt    string
}

// AutoCompactEvent reports that the loop compacted the
// conversation automatically — either because the visible
// token estimate entered the model's reserved generation space
// ("auto") or because the provider returned a context-length
// error ("context-limit").
type AutoCompactEvent struct {
	Removed        int    // messages removed/hidden
	Window         int    // resolved context window (tokens)
	Estimated      int    // effective next-request estimate before compaction
	RawEstimated   int    // local estimator before provider calibration
	EstimateSource string // "estimate" | "provider+delta"
	ExactBase      int    // last provider-reported prompt usage, if used
	Threshold      int    // automatic trigger in tokens
	WindowSource   string // config/provider/catalog/learned/fallback
	Reason         string // "auto" | "context-limit" | "manual"
}

func (AutoCompactEvent) event() {}

// ToolResultsPrunedEvent reports that old tool results were replaced
// in place with short markers (prune.go) — the zero-LLM first line of
// context defense that runs before the summary fallback. Reclaimed is
// the estimated token gain; Estimated/Window are the visible estimate
// before pruning and the resolved context window.
type ToolResultsPrunedEvent struct {
	Pruned    int // tool results replaced with markers
	Reclaimed int // estimated tokens reclaimed
	Estimated int // visible token estimate before the prune
	Window    int // resolved context window (tokens)
}

func (ToolResultsPrunedEvent) event() {}

// NoticeEvent is a non-terminal, informational status line from the
// provider layer (llm.Delta.Notice) — e.g. "rate limited, retrying
// in 2s". It is UI/log-only: the text is never appended to the
// conversation history, so the model never sees it.
type NoticeEvent struct {
	Text string
}

func (NoticeEvent) event() {}

// ErrorEvent is the terminal event of a failed run. Usage and Steps retain
// partial accounting so callers can report work already consumed before the
// failure (notably workers that hit MaxSteps).
type ErrorEvent struct {
	Err   error
	Usage Usage
	Steps int
}

func (ErrorEvent) event() {}

// Usage reports token accounting for one run. Providers fill this in;
// zero values mean "unknown" and must not be treated as a hard error.
type Usage struct {
	Input  int
	Output int
	Total  int
	// Cached is the sum of prompt tokens the backend served from a
	// KV/prompt cache across the run (provider cached_tokens). Zero
	// when the backend does not report it.
	Cached int
	// Reasoning is the sum of hidden chain-of-thought tokens across
	// the run (provider reasoning_tokens). Zero when not reported.
	Reasoning int
}

// Agent is the minimal interface every agent implementation must
// satisfy. The MVP noop agent in this file returns DoneEvent only;
// the real one in F1 will stream message and tool events.
type Agent interface {
	Name() string
	Run(ctx context.Context, prompt string) (<-chan Event, error)
}

// Noop is the placeholder agent used until the real loop is wired in.
// It exists so that wiring (main.go, TUI, tests) has something
// concrete to talk to from day one.
type Noop struct{ name string }

// NewNoop returns a Noop agent with the given name. Empty name is
// rejected because the agent name ends up in the database and on
// logs; "anonymous" is rarely a useful identity.
func NewNoop(name string) (*Noop, error) {
	if name == "" {
		return nil, fmt.Errorf("agent.NewNoop: name is empty")
	}
	return &Noop{name: name}, nil
}

// Name implements Agent.
func (n *Noop) Name() string { return n.name }

// Run implements Agent. It emits a single DoneEvent with zero usage
// after a short delay so callers can see the event stream work.
func (n *Noop) Run(ctx context.Context, prompt string) (<-chan Event, error) {
	if prompt == "" {
		return nil, fmt.Errorf("agent.Noop.Run: prompt is empty")
	}
	out := make(chan Event, 1)
	go func() {
		defer close(out)
		select {
		case <-ctx.Done():
			out <- ErrorEvent{Err: ctx.Err()}
		case <-time.After(10 * time.Millisecond):
			out <- DoneEvent{}
		}
	}()
	return out, nil
}
