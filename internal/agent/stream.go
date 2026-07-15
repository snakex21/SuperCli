package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

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
