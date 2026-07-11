// Package stats records per-turn metrics for a session and
// renders them via the --stats command. F2.g covers the basic
// recorder + printer; the on-disk format is a small JSON file
// inside the home directory so it can be inspected by hand.
package stats

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
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
	TotalSaved() int
	EndStep()
	Snapshot() []Turn
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
func (Noop) TotalSaved() int                   { return 0 }
func (Noop) EndStep()                          {}
func (Noop) Snapshot() []Turn                  { return nil }
func (Noop) Reset()                            {}

// Memory collects turns in process memory. Thread-safe.
type Memory struct {
	mu    sync.Mutex
	turns []Turn
	cur   *Turn
	saved int // F11 draft savings accumulator (per Memory instance)
}

// NewMemory returns a fresh Memory recorder.
func NewMemory() *Memory { return &Memory{} }

// StartStep begins a new turn with the given 1-based step
// number. Subsequent Record* calls accumulate into the turn
// until EndStep.
func (m *Memory) StartStep(step int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cur = &Turn{Step: step, StartedAt: time.Now().UTC(), Sources: map[string]int{}}
}

// RecordTokens sets the turn's token counts.
func (m *Memory) RecordTokens(in, out int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return
	}
	m.cur.TokensIn = in
	m.cur.TokensOut = out
}

// RecordTools replaces the turn's tools list with a unique,
// sorted set of the provided names.
func (m *Memory) RecordTools(names []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	sort.Strings(out)
	m.cur.Tools = out
}

// RecordToolCalls adds n to the turn's raw tool-call counter
// (duplicates count; see Turn.ToolCalls). Additive so a step
// that executes calls in more than one batch still sums up.
// No-op outside a step or for n <= 0.
func (m *Memory) RecordToolCalls(n int) {
	if n <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return
	}
	m.cur.ToolCalls += n
}

// RecordPhase adds d to the named phase of the current turn
// (microsecond resolution, see Turn.Phases). Accumulates on
// repeat so retried provider calls and multi-message persists
// sum up. No-op outside a step, for empty names, or d < 0.
func (m *Memory) RecordPhase(name string, d time.Duration) {
	if name == "" || d < 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return
	}
	if m.cur.Phases == nil {
		m.cur.Phases = map[string]int64{}
	}
	m.cur.Phases[name] += d.Microseconds()
}

// RecordSources replaces the turn's source token map.
func (m *Memory) RecordSources(src map[string]int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return
	}
	cp := make(map[string]int, len(src))
	for k, v := range src {
		cp[k] = v
	}
	m.cur.Sources = cp
}

// RecordSaved adds n to the current turn's TokensSaved
// counter (F11 draft savings). Negative values are
// ignored — savings are non-negative by definition.
// Calling outside of a step is a no-op. The running
// total is also kept on the Memory instance so
// tests/UI can read it without a per-turn Snapshot.
func (m *Memory) RecordSaved(n int) {
	if n <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saved += n
	if m.cur == nil {
		return
	}
	m.cur.TokensSaved += n
}

// RecordModel sets the current turn's model name.
func (m *Memory) RecordModel(model string) {
	if model == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return
	}
	m.cur.Model = model
}

// TotalSaved returns the cumulative F11 savings
// across all RecordSaved calls (including those
// outside any in-progress step).
func (m *Memory) TotalSaved() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saved
}

// EndStep finalises the current turn and appends it to the
// snapshot. It is a no-op when no turn is in progress.
func (m *Memory) EndStep() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return
	}
	m.cur.DurationMs = time.Since(m.cur.StartedAt).Milliseconds()
	m.turns = append(m.turns, *m.cur)
	m.cur = nil
}

// Snapshot returns a copy of the recorded turns.
func (m *Memory) Snapshot() []Turn {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Turn, len(m.turns))
	copy(out, m.turns)
	return out
}

// Reset drops all recorded turns.
func (m *Memory) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.turns = nil
	m.cur = nil
	m.saved = 0
}

// Save writes the snapshot to a JSON file at path. The file
// is overwritten.
func Save(path string, turns []Turn) error {
	if path == "" {
		return fmt.Errorf("stats.Save: path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(struct {
		Turns []Turn `json:"turns"`
	}{turns}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o644)
}

// Load reads a snapshot from a JSON file produced by Save.
// Missing file returns nil turns and no error.
func Load(path string) ([]Turn, error) {
	if path == "" {
		return nil, fmt.Errorf("stats.Load: path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var wrapped struct {
		Turns []Turn `json:"turns"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return nil, fmt.Errorf("stats.Load: %w", err)
	}
	return wrapped.Turns, nil
}

// Print writes a human-readable table of turns to w followed
// by a summary line. The format is intentionally plain (no
// external deps) so the --stats output works on every platform.
func Print(w io.Writer, turns []Turn) {
	if len(turns) == 0 {
		fmt.Fprintln(w, "no stats recorded")
		return
	}
	fmt.Fprintf(w, "%-4s %-8s %-8s %-9s %-6s %-8s %s\n", "step", "in", "out", "ms", "calls", "saved", "tools")
	for _, t := range turns {
		fmt.Fprintf(w, "%-4d %-8d %-8d %-9d %-6d %-8d %s\n",
			t.Step, t.TokensIn, t.TokensOut, t.DurationMs, t.ToolCalls, t.TokensSaved, joinTools(t.Tools))
		if p := FormatPhases(t.Phases); p != "" {
			fmt.Fprintf(w, "     %s\n", p)
		}
	}
	total := Sum(turns)
	fmt.Fprintf(w, "\ntotal: %d turns, %d in, %d out, %d combined, %d saved by drafts\n",
		total.Turns, total.TokensIn, total.TokensOut, total.TokensIn+total.TokensOut, total.TokensSaved)
	fmt.Fprintf(w, "tool calls: %d total, %.1f avg/step, %d step(s) with >1 call\n",
		total.ToolCalls, float64(total.ToolCalls)/float64(total.Turns), total.MultiCall)
	if p := FormatPhases(SumPhases(turns)); p != "" {
		fmt.Fprintf(w, "phase totals: %s\n", p)
	}
}

// FormatPhases renders a phase map (µs) as one compact line:
// canonical phases first in pipeline order, then any extra keys
// (per-tool entries) sorted. Zero-valued canonical phases are
// skipped. Returns "" when there is nothing to show.
func FormatPhases(phases map[string]int64) string {
	if len(phases) == 0 {
		return ""
	}
	parts := make([]string, 0, len(phases))
	seen := make(map[string]struct{}, len(phases))
	for _, k := range phaseOrder {
		seen[k] = struct{}{}
		if v, ok := phases[k]; ok {
			parts = append(parts, fmt.Sprintf("%s=%s", k, formatUs(v)))
		}
	}
	extra := make([]string, 0, len(phases))
	for k := range phases {
		if _, ok := seen[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		parts = append(parts, fmt.Sprintf("%s=%s", k, formatUs(phases[k])))
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

// formatUs renders microseconds human-readably: sub-millisecond
// values keep µs precision (the cheap phases must not round to
// zero — proving them cheap is the point), everything else is ms.
func formatUs(us int64) string {
	switch {
	case us < 1000:
		return fmt.Sprintf("%dµs", us)
	case us < 10_000:
		return fmt.Sprintf("%.1fms", float64(us)/1000)
	default:
		return fmt.Sprintf("%dms", us/1000)
	}
}

// PhaseLine renders one turn as a single machine-greppable line
// for batch mode stderr: [phase] step=N calls=N in=N out=N
// followed by the phase breakdown. Returns "" for a turn with
// no phase data so callers can skip silently.
func PhaseLine(t Turn) string {
	p := FormatPhases(t.Phases)
	if p == "" {
		return ""
	}
	return fmt.Sprintf("[phase] step=%d calls=%d in=%d out=%d %s",
		t.Step, t.ToolCalls, t.TokensIn, t.TokensOut, p)
}

func joinTools(tools []string) string {
	if len(tools) == 0 {
		return "-"
	}
	out := ""
	for i, n := range tools {
		if i > 0 {
			out += ","
		}
		out += n
	}
	return out
}
