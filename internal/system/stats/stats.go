// Package stats records per-turn metrics for a session and
// renders them via the --stats command. F2.g covers the basic
// recorder + printer; the on-disk format is a small JSON file
// inside the home directory so it can be inspected by hand.
package stats

import (
	"time"
)

// Turn is a single model call's worth of metrics. Step is
// 1-based; TokensIn/Out come from the provider; Tools is the
// list of tool names invoked during the turn (unique, sorted).
// Sources is the per-source token estimate at the time the turn
// started; F2.a populates it. TokensSaved is the F11 draft-
// model savings accumulated during this turn (>= 0).
// Model is the provider model used for this turn (F28).
//
// ToolCalls is the RAW number of tool calls the model emitted in
// this step (duplicates count, unlike the unique Tools list). It
// is the metric that decides whether read-only tool parallelism
// is worth building: if models emit 1 call/step, parallelism
// buys nothing.
//
// Phases is the per-step phase timing breakdown in MICROSECONDS
// (µs — millisecond granularity would round the cheap phases,
// exactly the ones we want proven cheap, down to zero). Keys are
// the phase* constants below plus per-tool "tool:<name>" entries.
// Repeated recordings of the same phase accumulate (e.g. the
// one-shot retry after a context-overflow compaction).
type Turn struct {
	Step        int              `json:"step"`
	TokensIn    int              `json:"tokens_in"`
	TokensOut   int              `json:"tokens_out"`
	Tools       []string         `json:"tools,omitempty"`
	ToolCalls   int              `json:"tool_calls"`
	Phases      map[string]int64 `json:"phases_us,omitempty"`
	Sources     map[string]int   `json:"sources,omitempty"`
	StartedAt   time.Time        `json:"started_at"`
	DurationMs  int64            `json:"duration_ms"`
	TokensSaved int              `json:"tokens_saved,omitempty"`
	Model       string           `json:"model,omitempty"`
}

// Canonical phase names for Turn.Phases. The agent loop records
// one wall-clock measurement per phase per step:
//
//	PhaseContextPrepare — prune/auto-compact/tool defs + provider
//	                      message assembly (everything before the
//	                      request leaves the client)
//	PhaseRequestEncode  — provider.Complete up to stream handoff
//	                      (request serialization + connection setup)
//	PhaseBackendWait    — time to FIRST delta from the backend (TTFT)
//	PhaseStreamTotal    — first delta → stream closed
//	PhaseToolExecution  — the step's whole tool-call batch
//	PhaseSessionPersist — session writes (overlaps other phases:
//	                      persisting happens inside the step, so it
//	                      is excluded from the NextTurnPrepare math)
//	PhaseNextTurnPrepare — step wall time not covered by the
//	                      disjoint phases above (eviction, draft
//	                      accounting, reflection, event plumbing)
const (
	PhaseContextPrepare  = "context_prepare"
	PhaseRequestEncode   = "request_encode"
	PhaseBackendWait     = "backend_wait"
	PhaseStreamTotal     = "stream_total"
	PhaseToolExecution   = "tool_execution"
	PhaseSessionPersist  = "session_persist"
	PhaseNextTurnPrepare = "next_turn_prepare"
)

// phaseOrder is the render order for the canonical phases.
var phaseOrder = []string{
	PhaseContextPrepare,
	PhaseRequestEncode,
	PhaseBackendWait,
	PhaseStreamTotal,
	PhaseToolExecution,
	PhaseSessionPersist,
	PhaseNextTurnPrepare,
}

// Call is one measured model invocation, labeled with its purpose
// ("main", "navigator", "compact", "reflection", "draft", "memory",
// ...). Every Provider.Complete call in the process flows here via
// the llm.Metered decorator, so helper inferences can no longer
// hide inside a CLI phase (the audit found a 13.9s model call
// booked as next_turn_prepare). Step is the 1-based step in
// progress when the call landed, 0 = outside any step (navigator
// pre-step classification, background memory saves).
type Call struct {
	Purpose    string    `json:"purpose"`
	Model      string    `json:"model,omitempty"`
	Provider   string    `json:"provider,omitempty"`
	Background bool      `json:"background,omitempty"`
	Canceled   bool      `json:"canceled,omitempty"`
	Failed     bool      `json:"failed,omitempty"`
	TTFTUs     int64     `json:"ttft_us,omitempty"`
	DurationUs int64     `json:"duration_us"`
	TokensIn   int       `json:"tokens_in"`
	TokensOut  int       `json:"tokens_out"`
	Step       int       `json:"step,omitempty"`
	StartedAt  time.Time `json:"started_at"`
}

// CallAgg is the per-purpose aggregate over a set of calls.
type CallAgg struct {
	Purpose    string
	Count      int
	Background int
	Canceled   int
	Failed     int
	TokensIn   int
	TokensOut  int
	TotalUs    int64
	TTFTUs     int64 // sum over calls that got a first delta
	TTFTCount  int   // calls contributing to TTFTUs
}

// SumCalls aggregates calls per purpose, in order of first
// appearance (the main call naturally sorts first in a normal
// session).
func SumCalls(calls []Call) []CallAgg {
	byPurpose := make(map[string]*CallAgg)
	order := make([]string, 0)
	for _, c := range calls {
		p := c.Purpose
		if p == "" {
			p = "unknown"
		}
		agg, ok := byPurpose[p]
		if !ok {
			agg = &CallAgg{Purpose: p}
			byPurpose[p] = agg
			order = append(order, p)
		}
		agg.Count++
		if c.Background {
			agg.Background++
		}
		if c.Canceled {
			agg.Canceled++
		}
		if c.Failed {
			agg.Failed++
		}
		agg.TokensIn += c.TokensIn
		agg.TokensOut += c.TokensOut
		agg.TotalUs += c.DurationUs
		if c.TTFTUs > 0 {
			agg.TTFTUs += c.TTFTUs
			agg.TTFTCount++
		}
	}
	out := make([]CallAgg, 0, len(order))
	for _, p := range order {
		out = append(out, *byPurpose[p])
	}
	return out
}

// Total summarises a set of turns.
type Total struct {
	TokensIn    int
	TokensOut   int
	Turns       int
	TokensSaved int
	ToolCalls   int // raw tool calls across all steps
	MultiCall   int // steps that emitted MORE than one tool call
}

// Sum returns the cumulative counters across all turns.
func Sum(turns []Turn) Total {
	var t Total
	for _, u := range turns {
		t.TokensIn += u.TokensIn
		t.TokensOut += u.TokensOut
		t.TokensSaved += u.TokensSaved
		t.ToolCalls += u.ToolCalls
		if u.ToolCalls > 1 {
			t.MultiCall++
		}
		t.Turns++
	}
	return t
}

// SumPhases returns the per-phase totals (µs) across turns.
func SumPhases(turns []Turn) map[string]int64 {
	out := map[string]int64{}
	for _, t := range turns {
		for k, v := range t.Phases {
			out[k] += v
		}
	}
	return out
}

// ModelBreakdown tracks in/out per model across turns.
type ModelBreakdown struct {
	Model string
	In    int
	Out   int
	Turns int
}

// SumByModel returns per-model token breakdowns.
func SumByModel(turns []Turn) []ModelBreakdown {
	byModel := make(map[string]*ModelBreakdown)
	order := make([]string, 0)
	for _, t := range turns {
		m := t.Model
		if m == "" {
			m = "unknown"
		}
		if _, ok := byModel[m]; !ok {
			byModel[m] = &ModelBreakdown{Model: m}
			order = append(order, m)
		}
		byModel[m].In += t.TokensIn
		byModel[m].Out += t.TokensOut
		byModel[m].Turns++
	}
	out := make([]ModelBreakdown, 0, len(order))
	for _, m := range order {
		out = append(out, *byModel[m])
	}
	return out
}

// Recorder is the interface the agent loop calls into. The
// default no-op implementation is Noop; the in-memory
// implementation is Memory.
type Recorder interface {
	StartStep(step int)
	RecordTokens(in, out int)
	RecordTools(names []string)
	RecordToolCalls(n int)
	RecordPhase(name string, d time.Duration)
	RecordSources(sources map[string]int)
	RecordSaved(saved int)
	RecordModel(model string)
	// RecordCall stores one purpose-labeled model invocation
	// (llm.Metered feeds this). Unlike the Record* methods above
	// it works OUTSIDE a step too — navigator classification and
	// background memory saves land here with Step 0.
	RecordCall(c Call)
	TotalSaved() int
	EndStep()
	Snapshot() []Turn
	// Calls returns a copy of every recorded model call.
	Calls() []Call
	Reset()
}

// Noop discards everything. Use it in tests and the "no
// telemetry" mode.
type Noop struct{}

// NewNoop returns a Noop recorder.
func NewNoop() *Noop { return &Noop{} }

func (Noop) StartStep(step int)                {}
func (Noop) RecordTokens(in, out int)          {}
func (Noop) RecordTools(names []string)        {}
func (Noop) RecordToolCalls(n int)             {}
func (Noop) RecordPhase(string, time.Duration) {}
func (Noop) RecordSources(map[string]int)      {}
func (Noop) RecordSaved(int)                   {}
func (Noop) RecordModel(string)                {}
func (Noop) RecordCall(Call)                   {}
func (Noop) TotalSaved() int                   { return 0 }
func (Noop) EndStep()                          {}
func (Noop) Snapshot() []Turn                  { return nil }
func (Noop) Calls() []Call                     { return nil }
func (Noop) Reset()                            {}

// Memory collects turns in process memory. Thread-safe.
