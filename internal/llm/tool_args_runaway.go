package llm

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// runawayToolArgsWindow is how much trailing tool-call argument text must be
// nothing but whitespace and JSON whitespace escapes before the stream is
// stopped. Live (gpt-5.4-nano on the Responses API) a ctx_execute call
// degenerated into " \n \n ..." and streamed ~70k output tokens over seven
// minutes before the benchmark harness killed it; nothing in SuperCli capped
// it. 8 KiB (~3k tokens) is far beyond any legitimate blank run in arguments.
const runawayToolArgsWindow = 8 << 10

// Degenerate-repetition guard (1.0.28 incident: gpt-6-luna streamed a
// 92-byte non-whitespace block — "RENDERING_ARGS####json 1/1 (1)####..." —
// into invoke_tool arguments for 739 s, ~267k chars, until the 128k output
// cap; only ~17 % of it was whitespace, so the whitespace guard never fired).
const (
	// maxToolCallArgumentBytes is the hard cap on one tool call's arguments.
	// Larger edits must be split; the agent's runaway retry says so.
	maxToolCallArgumentBytes = 256 << 10
	// degenerateArgsWindow is the trailing span the repetition check inspects.
	degenerateArgsWindow = 16 << 10
	// degenerateArgsCheckEvery runs the check once per 4 KiB of growth.
	degenerateArgsCheckEvery = 4 << 10
	// degenerateMinDistinctGrams: a window with fewer distinct 16-byte
	// substrings is a short loop (any string with period p has at most p).
	// Real code, prose, CSV/JSON tables and base64 have thousands.
	degenerateMinDistinctGrams = 1024
	degenerateGram             = 16
	// degenerateMaxPeriod / degenerateMinRepeats: an exact repetition of a
	// unit of up to 2 KiB, at least 8 times and over at least
	// degenerateMinRepeatSpan bytes, ending at the newest byte. This catches
	// periods too long for the distinct-substring count.
	degenerateMaxPeriod     = 2 << 10
	degenerateMinRepeats    = 8
	degenerateMinRepeatSpan = 8 << 10
)

// Runaway reasons (RunawayToolCallError.Reason).
const (
	RunawayWhitespace = "whitespace"
	RunawayRepetition = "repetition"
	RunawayTooLarge   = "size"
	RunawayTooSlow    = "time"
)

// checkRunawayToolArguments inspects args after a delta grew it from prevLen.
// Every check is gated on crossing a size boundary, so the hot path stays a
// length comparison.
func checkRunawayToolArguments(name string, args *strings.Builder, prevLen int) error {
	n := args.Len()
	if n > maxToolCallArgumentBytes {
		return &RunawayToolCallError{Tool: name, KB: n >> 10, Reason: RunawayTooLarge}
	}
	if n < runawayToolArgsWindow {
		return nil
	}
	s := args.String()
	if n>>10 != prevLen>>10 && isWhitespaceRun(s[n-runawayToolArgsWindow:]) {
		return &RunawayToolCallError{Tool: name, KB: n >> 10, Reason: RunawayWhitespace}
	}
	return checkDegenerateGrowth(name, s, prevLen)
}

// checkFullToolArguments applies the same guard to arguments that arrived in
// one piece (Responses function_call_arguments.done / output_item.done
// without deltas, or a gateway's single-chunk call).
func checkFullToolArguments(name, args string) error {
	if len(args) > maxToolCallArgumentBytes {
		return &RunawayToolCallError{Tool: name, KB: len(args) >> 10, Reason: RunawayTooLarge}
	}
	if len(args) >= runawayToolArgsWindow && isWhitespaceRun(args[len(args)-runawayToolArgsWindow:]) {
		return &RunawayToolCallError{Tool: name, KB: len(args) >> 10, Reason: RunawayWhitespace}
	}
	return checkDegenerateGrowth(name, args, 0)
}

// checkDegenerateGrowth runs the repetition check at every 4 KiB boundary the
// arguments crossed since prevLen (one boundary for an ordinary delta), plus
// the current end, so a single huge chunk cannot hide a degenerate middle.
func checkDegenerateGrowth(name, s string, prevLen int) error {
	n := len(s)
	if n < degenerateArgsWindow || n/degenerateArgsCheckEvery == prevLen/degenerateArgsCheckEvery {
		return nil
	}
	first := (prevLen/degenerateArgsCheckEvery + 1) * degenerateArgsCheckEvery
	if first < degenerateArgsWindow {
		first = degenerateArgsWindow
	}
	for end := first; end <= n; end += degenerateArgsCheckEvery {
		if degenerateWindow(s[end-degenerateArgsWindow : end]) {
			return &RunawayToolCallError{Tool: name, KB: n >> 10, Reason: RunawayRepetition}
		}
	}
	if n%degenerateArgsCheckEvery != 0 && degenerateWindow(s[n-degenerateArgsWindow:]) {
		return &RunawayToolCallError{Tool: name, KB: n >> 10, Reason: RunawayRepetition}
	}
	return nil
}

// DegenerateToolArguments reports whether complete arguments would be
// rejected by the streaming guard (too large, whitespace run or a repeating
// block). The agent uses it to keep such arguments out of history.
func DegenerateToolArguments(args string) bool {
	return checkFullToolArguments("", args) != nil
}

func isWhitespaceRun(tail string) bool {
	for i := 0; i < len(tail); i++ {
		switch tail[i] {
		case ' ', '\t', '\r', '\n':
		case '\\':
			if i+1 < len(tail) {
				switch tail[i+1] {
				case 'n', 'r', 't', '\\':
					i++
				}
			}
		case 'n', 'r', 't':
			// The second byte of an escape split across the window edge.
			if i != 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// degenerateWindow reports whether w (the trailing window) is a short loop:
// fewer than degenerateMinDistinctGrams distinct 16-byte substrings, or an
// exact repetition (>= 8 times, >= 8 KiB) of a unit of up to 2 KiB at its end.
// Legitimate content exits the distinct count after ~1k positions.
func degenerateWindow(w string) bool {
	if len(w) < degenerateGram {
		return false
	}
	seen := make(map[[2]uint64]struct{}, degenerateMinDistinctGrams)
	distinct := false
	for i := 0; i+degenerateGram <= len(w); i++ {
		key := [2]uint64{le64(w[i : i+8]), le64(w[i+8 : i+16])}
		seen[key] = struct{}{}
		if len(seen) >= degenerateMinDistinctGrams {
			distinct = true
			break
		}
	}
	if !distinct {
		return true
	}
	return trailingRepetition(w)
}

// trailingRepetition finds a period p <= degenerateMaxPeriod such that the
// last max(8p, degenerateMinRepeatSpan) bytes of w repeat with period p.
// Candidates are the earlier occurrences of w's last 16 bytes, nearest first.
func trailingRepetition(w string) bool {
	n := len(w)
	if n < degenerateMinRepeatSpan {
		return false
	}
	suffix := w[n-degenerateGram:]
	lo := n - degenerateGram - degenerateMaxPeriod
	if lo < 0 {
		lo = 0
	}
	hi := n - 1 // an occurrence must start before the suffix itself
	for hi-lo >= degenerateGram {
		j := strings.LastIndex(w[lo:hi], suffix)
		if j < 0 {
			return false
		}
		j += lo
		p := n - degenerateGram - j
		span := degenerateMinRepeats * p
		if span < degenerateMinRepeatSpan {
			span = degenerateMinRepeatSpan
		}
		if span <= n && w[n-span+p:] == w[n-span:n-p] {
			return true
		}
		hi = j + degenerateGram - 1
	}
	return false
}

func le64(s string) uint64 {
	_ = s[7]
	return uint64(s[0]) | uint64(s[1])<<8 | uint64(s[2])<<16 | uint64(s[3])<<24 |
		uint64(s[4])<<32 | uint64(s[5])<<40 | uint64(s[6])<<48 | uint64(s[7])<<56
}

// RunawayToolCallError reports a tool call whose streamed arguments
// degenerated (whitespace run, repeating block), exceeded the size cap or
// streamed for too long; the request was cancelled and the call was never
// executed, so the agent may safely ask for it again.
type RunawayToolCallError struct {
	Tool string
	KB   int
	// Reason is one of the Runaway* constants; empty means whitespace
	// (the historical meaning).
	Reason string
	// Elapsed is set for RunawayTooSlow.
	Elapsed time.Duration
}

func (e *RunawayToolCallError) Error() string {
	switch e.Reason {
	case RunawayRepetition:
		return fmt.Sprintf("stopped a runaway tool call: the model streamed %d KB of a repeating block into %q arguments; the request was cancelled to stop paying for more output", e.KB, e.Tool)
	case RunawayTooLarge:
		return fmt.Sprintf("stopped a runaway tool call: %q arguments exceeded %d KB (%d KB streamed); the request was cancelled to stop paying for more output", e.Tool, maxToolCallArgumentBytes>>10, e.KB)
	case RunawayTooSlow:
		return fmt.Sprintf("stopped a runaway tool call: %q arguments were still streaming after %s (%d KB); the request was cancelled to stop paying for more output", e.Tool, e.Elapsed.Round(time.Second), e.KB)
	}
	return fmt.Sprintf("stopped a runaway tool call: the model streamed %d KB of only whitespace into %q arguments; the request was cancelled to stop paying for more output", e.KB, e.Tool)
}

// Describe is a short phrase for notices ("12 KB of whitespace").
func (e *RunawayToolCallError) Describe() string {
	switch e.Reason {
	case RunawayRepetition:
		return fmt.Sprintf("%d KB of a repeating block", e.KB)
	case RunawayTooLarge:
		return fmt.Sprintf("arguments over %d KB", maxToolCallArgumentBytes>>10)
	case RunawayTooSlow:
		return fmt.Sprintf("arguments still streaming after %s", e.Elapsed.Round(time.Second))
	}
	return fmt.Sprintf("%d KB of whitespace", e.KB)
}

// DefaultToolCallStreamLimit bounds how long ONE model call may keep
// streaming tool-call arguments (SUPERCLI_LLM_TOOL_STREAM_TIMEOUT).
const DefaultToolCallStreamLimit = 10 * time.Minute

// toolCallStreamNoticeEvery is the progress-notice interval while a long
// tool call is being received.
const toolCallStreamNoticeEvery = 30 * time.Second

var toolCallStreamLimit atomic.Int64

func init() { toolCallStreamLimit.Store(int64(DefaultToolCallStreamLimit)) }

// SetToolCallStreamLimit installs the per-call wall-time cap on streaming
// tool-call arguments; d <= 0 disables it.
func SetToolCallStreamLimit(d time.Duration) { toolCallStreamLimit.Store(int64(d)) }

// ToolCallStreamLimit reports the current cap (0 = disabled).
func ToolCallStreamLimit() time.Duration {
	d := time.Duration(toolCallStreamLimit.Load())
	if d < 0 {
		return 0
	}
	return d
}

// toolStreamClock is a test seam.
var toolStreamClock = time.Now

// toolStreamWatch times the tool-call argument phase of one model call:
// progress notices every toolCallStreamNoticeEvery, and a RunawayTooSlow
// error once ToolCallStreamLimit has passed.
type toolStreamWatch struct {
	start, lastNotice time.Time
}

func (w *toolStreamWatch) observe(name string, argBytes int) (notice string, err error) {
	now := toolStreamClock()
	if w.start.IsZero() {
		w.start, w.lastNotice = now, now
		return "", nil
	}
	elapsed := now.Sub(w.start)
	if limit := ToolCallStreamLimit(); limit > 0 && elapsed > limit {
		return "", &RunawayToolCallError{Tool: name, KB: argBytes >> 10, Reason: RunawayTooSlow, Elapsed: elapsed}
	}
	if now.Sub(w.lastNotice) >= toolCallStreamNoticeEvery {
		w.lastNotice = now
		return fmt.Sprintf("still receiving the %s call from the model: %d KB of arguments in %s", name, argBytes>>10, elapsed.Round(time.Second)), nil
	}
	return "", nil
}

// Tool-argument progress (1.0.38): a large create_file/patch_file call can
// take minutes to generate; before, the GUI showed only "model thinking" with
// a growing "last activity" while kilobytes were arriving. One progress
// delta once a call reaches toolArgsProgressMin bytes (small calls finish
// before anyone could read a label), then every toolArgsProgressStep bytes
// or toolArgsProgressEvery, whichever comes first.
const (
	toolArgsProgressMin   = 1 << 10
	toolArgsProgressStep  = 2 << 10
	toolArgsProgressEvery = time.Second
)

type toolArgsProgress struct {
	name  string
	bytes int
	at    time.Time
}

// next returns a progress report to emit, or nil when throttled.
func (p *toolArgsProgress) next(name string, bytes int) *ToolArgsProgress {
	if bytes < toolArgsProgressMin {
		return nil
	}
	now := toolStreamClock()
	if name == p.name && !p.at.IsZero() {
		if bytes <= p.bytes || (bytes < p.bytes+toolArgsProgressStep && now.Sub(p.at) < toolArgsProgressEvery) {
			return nil
		}
	}
	p.name, p.bytes, p.at = name, bytes, now
	return &ToolArgsProgress{Name: name, Bytes: bytes}
}
