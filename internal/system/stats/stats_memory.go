// Package stats records per-turn metrics for a session and
// renders them via the --stats command. F2.g covers the basic
// recorder + printer; the on-disk format is a small JSON file
// inside the home directory so it can be inspected by hand.
package stats

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Memory struct {
	mu    sync.Mutex
	turns []Turn
	cur   *Turn
	calls []Call
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

// RecordCall appends one model-call record. When a step is in
// progress its number is stamped onto the record (best effort —
// background calls may land between steps, keeping Step 0).
func (m *Memory) RecordCall(c Call) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.Step == 0 && m.cur != nil {
		c.Step = m.cur.Step
	}
	m.calls = append(m.calls, c)
}

// Calls returns a copy of the recorded model calls.
func (m *Memory) Calls() []Call {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Call, len(m.calls))
	copy(out, m.calls)
	return out
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

// Reset drops all recorded turns and calls.
func (m *Memory) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.turns = nil
	m.cur = nil
	m.calls = nil
	m.saved = 0
}

// Save writes the snapshot to a JSON file at path. The file
// is overwritten. calls may be nil (old snapshots stay valid).
func Save(path string, turns []Turn, calls []Call) error {
	if path == "" {
		return fmt.Errorf("stats.Save: path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(struct {
		Turns []Turn `json:"turns"`
		Calls []Call `json:"calls,omitempty"`
	}{turns, calls}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o644)
}

// Load reads a snapshot from a JSON file produced by Save.
// Missing file returns nil turns/calls and no error. Snapshots
// written before the calls field existed load with nil calls.
func Load(path string) ([]Turn, []Call, error) {
	if path == "" {
		return nil, nil, fmt.Errorf("stats.Load: path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var wrapped struct {
		Turns []Turn `json:"turns"`
		Calls []Call `json:"calls"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return nil, nil, fmt.Errorf("stats.Load: %w", err)
	}
	return wrapped.Turns, wrapped.Calls, nil
}

// Print writes a human-readable table of turns to w followed
// by a summary line. The format is intentionally plain (no
// external deps) so the --stats output works on every platform.
