package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"supercli/internal/llm"
)

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

// stepLimitProgress is deliberately separate from reflection tracking.
// Reflection resets after each checkpoint; soft-limit decisions must retain
// the previous batch so an exact tool-call loop never earns extra steps.
// Only a successful mutation or execution batch can extend the soft limit.
// Discovery, unknown tools, and failed mutations never count as progress.
// A batch may buy extra budget once per Run: remembering only the previous
// batch let an A-B-A-B alternation collect a bonus on every single step.
type stepLimitProgress struct {
	granted map[[sha256.Size]byte]bool
}

func (p *stepLimitProgress) observe(calls []llm.ToolCall, failures, inert int) bool {
	if len(calls) == 0 || failures > 0 {
		return false
	}
	// Soft step extension only after a clean mutation/execution batch.
	// Unknown tools, pure discovery and edits that changed nothing new
	// must not buy more budget.
	if !batchHasSuccessfulProgress(calls, failures, inert) {
		return false
	}
	fingerprint := toolCallBatchFingerprint(calls)
	if p.granted[fingerprint] {
		return false
	}
	if p.granted == nil {
		p.granted = make(map[[sha256.Size]byte]bool)
	}
	p.granted[fingerprint] = true
	return true
}

// discoveryCallSoftLimit: after this many non-progress tool calls, inject
// a strategy nudge (no extra model router). Counted per tool call, not per step.
const discoveryCallSoftLimit = 12

// discoveryCallHardLimit: after this many non-progress tool calls (or a second
// soft threshold crossing), force a user-facing answer and stop tooling.
const discoveryCallHardLimit = 20

// cycleHistorySize is how many recent call fingerprints we keep to detect
// short A-B-A-B loops, not only consecutive identical calls.
const cycleHistorySize = 8

// discoverySignal is what discoveryProgress.observe returns to the step loop.
type discoverySignal int

const (
	discoveryNone       discoverySignal = iota
	discoveryNudge                      // first soft threshold: strategy message
	discoveryForceReply                 // hard threshold / second strike: answer user
)

// discoveryProgress tracks discovery-without-progress across a Run.
// Progress = successful mutation or successful execution only.
type discoveryProgress struct {
	// callStreak counts individual non-progress tool calls (not batches).
	callStreak int
	// nudges is how many strategy nudges already fired this Run.
	nudges int
	// recent holds normalized fingerprints of the last N calls for cycle detect.
	recent [][sha256.Size]byte
}

// observe updates counters from a finished tool batch.
// failures is the number of failed calls in the batch (post-verification).
func (p *discoveryProgress) observe(calls []llm.ToolCall, failures, inert int) discoverySignal {
	if len(calls) == 0 {
		return discoveryNone
	}
	// Successful mutation/execution anywhere in the batch is real progress.
	if batchHasSuccessfulProgress(calls, failures, inert) {
		p.callStreak = 0
		p.recent = p.recent[:0]
		return discoveryNone
	}

	// Count every call in a non-progress batch toward the streak.
	// Mixed batch with failed patch still counts all calls as non-progress.
	p.callStreak += len(calls)

	// Record fingerprints for cycle detection (A-B-A-B).
	for _, c := range calls {
		fp := toolCallFingerprint(c.Name, c.Arguments)
		p.recent = append(p.recent, fp)
		if len(p.recent) > cycleHistorySize {
			p.recent = p.recent[len(p.recent)-cycleHistorySize:]
		}
	}

	if p.hasShortCycle() {
		// Cycles are worse than linear discovery: escalate immediately.
		p.callStreak = discoveryCallHardLimit
	}

	if p.callStreak >= discoveryCallHardLimit || p.nudges >= 1 && p.callStreak >= discoveryCallSoftLimit {
		return discoveryForceReply
	}
	if p.callStreak >= discoveryCallSoftLimit {
		return discoveryNudge
	}
	return discoveryNone
}

func (p *discoveryProgress) noteNudge() {
	p.nudges++
	// After a soft nudge, give a short window before hard force, but keep
	// partial streak so the model cannot reset by one more list_dir.
	if p.callStreak > discoveryCallSoftLimit/2 {
		p.callStreak = discoveryCallSoftLimit / 2
	}
}

func (p *discoveryProgress) reset() {
	p.callStreak = 0
	p.recent = p.recent[:0]
}

// hasShortCycle detects short loops in recent fingerprints:
// period-1 (A A A), period-2 (A B A B), period-3 (A B C A B C).
// Fingerprints are name+normalized-args only (no tool_call_id).
func (p *discoveryProgress) hasShortCycle() bool {
	n := len(p.recent)
	if n < 3 {
		return false
	}
	// period-1: last three identical
	if p.recent[n-1] == p.recent[n-2] && p.recent[n-2] == p.recent[n-3] {
		return true
	}
	// period-2 needs 4; period-3 needs 6
	for period := 2; period <= 3; period++ {
		need := period * 2
		if n < need {
			continue
		}
		base := n - need
		ok := true
		// First half must not be all the same (would be period-1).
		allSame := true
		for i := 1; i < period; i++ {
			if p.recent[base+i] != p.recent[base] {
				allSame = false
				break
			}
		}
		if allSame {
			continue
		}
		for i := 0; i < period; i++ {
			if p.recent[base+i] != p.recent[base+period+i] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// toolKind classifies a tool for progress accounting.
// Unknown tools default to "other" and NEVER count as soft-limit progress
// and NEVER reset the discovery streak (only successful mutation/execution do).
func toolKind(name string) string {
	switch name {
	case "tool_search", "search_code", "list_dir", "read_lines", "read_many",
		"read_context", "read_image", "read_output", "read_pdf", "read_docx",
		"read_xlsx", "read_zip", "web_lookup", "web_search", "web_fetch",
		"invoke_tool", "recall", "code_intel", "process_session", "apply_skill",
		"scratchpad", "mcp_bridge", "search_history":
		return "discovery"
	case "patch_file", "create_file", "edit_line", "edit_lines", "insert_after",
		"delete_lines", "write_file", "make_dir", "move", "copy", "trash",
		"edit_docx", "edit_xlsx":
		return "mutation"
	case "ctx_execute":
		return "execution"
	default:
		return "other"
	}
}

func batchIsDiscoveryOnly(calls []llm.ToolCall) bool {
	if len(calls) == 0 {
		return false
	}
	for _, c := range calls {
		if toolKind(c.Name) != "discovery" {
			return false
		}
	}
	return true
}

// batchHasSuccessfulProgress is true only when the batch contains at least one
// productive mutation or execution call AND the batch had zero failures. A
// failed patch must not reset discovery counters or extend step budget.
//
// inert is how many of those calls succeeded without producing anything new —
// today a patch that only duplicated text the file already contained. Such a
// call is a write, not progress; counting it as progress is what kept a
// self-repeating edit loop funded.
func batchHasSuccessfulProgress(calls []llm.ToolCall, failures, inert int) bool {
	if failures > 0 || len(calls) == 0 {
		return false
	}
	productive := 0
	for _, c := range calls {
		switch toolKind(c.Name) {
		case "mutation", "execution":
			productive++
		}
	}
	return productive > inert
}

// identicalFailureGate blocks a third identical failed tool call (same
// name+normalized-args) within a Run. Two failures are allowed so the model
// can see the diagnostic; the third is rejected before Execute.
type identicalFailureGate struct {
	counts map[[sha256.Size]byte]int
}

func (g *identicalFailureGate) key(name, args string) [sha256.Size]byte {
	return toolCallFingerprint(name, args)
}

func (g *identicalFailureGate) shouldBlock(name, args string) bool {
	if g.counts == nil {
		return false
	}
	return g.counts[g.key(name, args)] >= 2
}

func (g *identicalFailureGate) recordFailure(name, args string) {
	if g.counts == nil {
		g.counts = make(map[[sha256.Size]byte]int)
	}
	k := g.key(name, args)
	g.counts[k]++
}

// attempts returns how many consecutive failures this exact call has
// accumulated in the current Run (0 = none recorded yet). Read after
// recordFailure it is the attempt number of the failure just seen.
func (g *identicalFailureGate) attempts(name, args string) int {
	if g.counts == nil {
		return 0
	}
	return g.counts[g.key(name, args)]
}

func (g *identicalFailureGate) recordSuccess(name, args string) {
	if g.counts == nil {
		return
	}
	delete(g.counts, g.key(name, args))
}

// repeatedMutationLimit is how many identical successful mutations one Run may
// apply before the next one is refused. Two are conceivable (an edit undone in
// between, a deliberate second identical append); a third is a loop, never a
// plan. Set against a real session that sent the same successful patch_file 23
// times because nothing in the loop counted successes.
const repeatedMutationLimit = 2

// identicalSuccessGate is the mirror of identicalFailureGate for calls that
// SUCCEED. Only mutations are counted: repeating a read (the same read_lines
// range after the file changed, a re-listed directory) is ordinary work, while
// re-applying the identical edit is not. Arguments are fingerprinted, so the
// gate sees through key reordering and refreshed advisory fields.
type identicalSuccessGate struct {
	counts map[[sha256.Size]byte]int
}

func (g *identicalSuccessGate) shouldBlock(name, args string) bool {
	if g.counts == nil || toolKind(name) != "mutation" {
		return false
	}
	return g.counts[toolCallFingerprint(name, args)] >= repeatedMutationLimit
}

func (g *identicalSuccessGate) recordSuccess(name, args string) {
	if toolKind(name) != "mutation" {
		return
	}
	if g.counts == nil {
		g.counts = make(map[[sha256.Size]byte]int)
	}
	g.counts[toolCallFingerprint(name, args)]++
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

	if p.failedBatchStreak >= 2 {
		return "repeated_tool_failure"
	}
	if p.repeatedBatches >= 2 {
		return "repeated_tool_batch"
	}
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

// advisoryArgKeys are top-level argument fields that carry optimistic
// concurrency data rather than the identity of the requested action. The model
// refreshes them between attempts (patch_file's base_hash becomes the previous
// call's after_hash), so keeping them in the fingerprint made a byte-identical
// edit look like a brand-new call every time — measured on a real session, 19
// of 20 consecutive repeats were invisible to every repeat detector. Dropped
// only at the top level: nested user data with the same key name is content.
var advisoryArgKeys = map[string]bool{
	"base_hash": true,
}

// normalizeToolArgsJSON reorders object keys so semantically identical
// argument objects hash the same regardless of key order, and drops advisory
// fields that do not change what the call actually does.
func normalizeToolArgsJSON(args string) string {
	args = strings.TrimSpace(args)
	if args == "" || args == "null" {
		return "{}"
	}
	var v any
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		return args
	}
	if obj, ok := v.(map[string]any); ok {
		for k := range advisoryArgKeys {
			delete(obj, k)
		}
	}
	norm, err := json.Marshal(canonicalizeJSON(v))
	if err != nil {
		return args
	}
	return string(norm)
}

func canonicalizeJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		// Encode as ordered array of [key, value] pairs so Marshal is stable.
		// Using a sorted-key object via json.Marshal of map is NOT stable in
		// all Go versions for nested maps; we re-build via sorted keys string.
		// Simpler: rebuild map iteration is random — so produce ordered slice.
		pairs := make([]any, 0, len(keys))
		for _, k := range keys {
			pairs = append(pairs, []any{k, canonicalizeJSON(t[k])})
		}
		return pairs
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = canonicalizeJSON(t[i])
		}
		return out
	default:
		return v
	}
}

func toolCallFingerprint(name, args string) [sha256.Size]byte {
	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte{0})
	h.Write([]byte(normalizeToolArgsJSON(args)))
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func toolCallBatchFingerprint(calls []llm.ToolCall) [sha256.Size]byte {
	h := sha256.New()
	for _, call := range calls {
		fp := toolCallFingerprint(call.Name, call.Arguments)
		h.Write(fp[:])
		h.Write([]byte{0xff})
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// messagesHaveRecentToolResult is true when the conversation tail still
// holds a tool result the model has not summarized for the user.
// Walks backward past empty assistants / system nudges.
func messagesHaveRecentToolResult(msgs []llm.Message) bool {
	for i := len(msgs) - 1; i >= 0; i-- {
		switch msgs[i].Role {
		case llm.RoleTool:
			return true
		case llm.RoleUser:
			return false
		case llm.RoleAssistant:
			if strings.TrimSpace(msgs[i].Content) != "" || len(msgs[i].Parts) > 0 {
				if len(msgs[i].ToolCalls) > 0 && strings.TrimSpace(msgs[i].Content) == "" && len(msgs[i].Parts) == 0 {
					continue
				}
				if len(msgs[i].ToolCalls) == 0 {
					return false
				}
			}
		}
	}
	return false
}

func (l *Loop) runReflection(ctx context.Context, step int, reason string, out chan<- Event) {
	if l.reflector == nil {
		return
	}
	reflStart := time.Now()
	txt, err := l.reflector.Reflect(llm.WithPurpose(ctx, llm.PurposeReflect), l.Messages)
	l.recordAuxWall(llm.PurposeReflect, time.Since(reflStart))
	if err != nil || strings.TrimSpace(txt) == "" {
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
