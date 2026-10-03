package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

// truncatedToolResponseError marks a completed response whose tool arguments
// cannot be trusted. Keep consuming through the usage frame before returning it.
type truncatedToolResponseError struct{}

func (*truncatedToolResponseError) Error() string {
	return "provider response reached the output token limit"
}

func (l *Loop) consume(ctx context.Context, stream <-chan llm.Delta, out chan<- Event) (string, []llm.ToolCall, *llm.Usage, error) {
	var toolCalls []llm.ToolCall
	var textCallIndexes []int
	var usage *llm.Usage
	truncated := false
	sc := newToolCallScanner()
	var transcript strings.Builder
	reasoningOpen := false
	closeReasoning := func() {
		if !reasoningOpen {
			return
		}
		transcript.WriteString("</thinking>\n")
		reasoningOpen = false
	}
	emitTo := func(end int) error {
		if end <= sc.emitted {
			return nil
		}
		text := sc.buf.String()[sc.emitted:end]
		closeReasoning()
		select {
		case out <- MessageEvent{Text: text}:
			transcript.WriteString(text)
			sc.emitted = end
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// backend_wait (TTFT) is ONE timestamp taken at the first model output;
	// stream_total is one measurement at stream close. Nothing is
	// timed per-delta — the streaming hot path pays a single zero-
	// value comparison per delta and no allocations.
	waitStart := time.Now()
	var firstDelta time.Time
	l.lastCallTTFT = 0
	l.lastCallGeneration = 0
	defer func() {
		if firstDelta.IsZero() {
			// The stream ended (or errored) before any model output:
			// the whole wait was backend time.
			l.recordWallPhase(stats.PhaseBackendWait, time.Since(waitStart))
			return
		}
		l.lastCallGeneration = time.Since(firstDelta)
		l.recordWallPhase(stats.PhaseStreamTotal, l.lastCallGeneration)
	}()
	for d := range stream {
		// Role, usage, retry notices and terminal frames are not generated
		// output and must not make a long backend wait look instant.
		if firstDelta.IsZero() && d.HasModelOutput() {
			firstDelta = time.Now()
			l.lastCallTTFT = firstDelta.Sub(waitStart)
			l.recordWallPhase(stats.PhaseBackendWait, firstDelta.Sub(waitStart))
		}
		if err := ctx.Err(); err != nil {
			closeReasoning()
			return transcript.String(), toolCalls, usage, err
		}
		if d.NativeReasoning != nil {
			l.nativeReasoning = append(l.nativeReasoning, d.NativeReasoning)
		}
		if d.Err != nil {
			closeReasoning()
			return transcript.String(), toolCalls, usage, d.Err
		}
		if d.Notice != "" {
			// Informational status (rate-limit retry etc.) — surface
			// to the UI, never into the conversation text.
			select {
			case out <- NoticeEvent{Text: d.Notice}:
			case <-ctx.Done():
				closeReasoning()
				return transcript.String(), toolCalls, usage, ctx.Err()
			}
			continue
		}
		if d.Reasoning != "" {
			if !reasoningOpen {
				transcript.WriteString("<thinking>")
				reasoningOpen = true
			}
			select {
			case out <- ReasoningEvent{Text: d.Reasoning}:
				transcript.WriteString(d.Reasoning)
			case <-ctx.Done():
				closeReasoning()
				return transcript.String(), toolCalls, usage, ctx.Err()
			}
		}
		if d.Content != "" {
			sc.append(d.Content)

			// Drain every complete block, including multiple calls delivered in
			// one delta. Retain the suffix: it may contain another complete call,
			// a partial marker, or prose. Previously reset(before) dropped it.
			for sc.xmlReady() || sc.sentReady() {
				var calls []llm.ToolCall
				var before, closeTag string
				var openAt int
				if sc.xmlReady() && (!sc.sentReady() || sc.xmlOpen < sc.sentOpen) {
					calls, before = extractXMLToolCalls(sc.buf.String())
					openAt, closeTag = sc.xmlOpen, "</tool_call>"
				} else {
					calls, before = extractSentinelToolCalls(sc.buf.String())
					openAt, closeTag = sc.sentOpen, sentinelClose
				}
				text := sc.buf.String()
				end := openAt + strings.Index(text[openAt:], closeTag) + len(closeTag)
				if len(calls) == 0 {
					// Preserve malformed output as text, then inspect the remaining
					// suffix. A bad block must not disable this protocol for the turn.
					if err := emitTo(end); err != nil {
						closeReasoning()
						return transcript.String(), toolCalls, usage, err
					}
					sc.reset(text[end:])
					continue
				}
				if err := emitTo(len(before)); err != nil {
					closeReasoning()
					return transcript.String(), toolCalls, usage, err
				}
				for i := range calls {
					textCallIndexes = append(textCallIndexes, len(toolCalls)+i)
				}
				toolCalls = append(toolCalls, calls...)
				// Leading prose was emitted above; retain only the unconsumed tail.
				sc.reset(text[end:])
			}

			if err := emitTo(sc.safeEmitEnd()); err != nil {
				closeReasoning()
				return transcript.String(), toolCalls, usage, err
			}
		}
		if d.ToolCall != nil {
			toolCalls = append(toolCalls, *d.ToolCall)
		}
		if d.Usage != nil {
			usage = d.Usage
		}
		// Anthropic normalizes max_tokens to length; compatible gateways may
		// preserve the native spelling. A later usage/stop frame cannot undo it.
		if d.FinishReason == "length" || d.FinishReason == "max_tokens" {
			truncated = true
		}
	}
	// No complete tool block claimed the retained suffix: surface it as plain
	// text (including malformed/incomplete markers) rather than losing output.
	if err := emitTo(sc.buf.Len()); err != nil {
		closeReasoning()
		return transcript.String(), toolCalls, usage, err
	}
	closeReasoning()
	calls := l.coalesceMirroredReads(toolCalls, textCallIndexes)
	if truncated && len(calls) > 0 {
		return transcript.String(), calls, usage, &truncatedToolResponseError{}
	}
	return transcript.String(), calls, usage, nil
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
			pairs = append(pairs, fmt.Sprintf(`%s:""`, quoteToolJSONString(paramName)))
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
		pairs = append(pairs, fmt.Sprintf(`%s:%s`, quoteToolJSONString(paramName), jsonString(value)))
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
				ID:        syntheticToolCallID("xml", name),
				Name:      name,
				Arguments: blob,
			}}
		}
	}

	args := "{" + strings.Join(pairs, ",") + "}"
	return []llm.ToolCall{{
		ID:        syntheticToolCallID("xml", name),
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
	return quoteToolJSONString(v)
}

// quoteToolJSONString encodes a literal key or value without expanding HTML.
// Structured argument values are handled separately by jsonString.
func quoteToolJSONString(v string) string {
	if !utf8.ValidString(v) {
		encoded, _ := json.Marshal(v)
		return string(encoded)
	}
	const hex = "0123456789abcdef"
	var out strings.Builder
	out.Grow(len(v) + 2)
	out.WriteByte('"')
	start := 0
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		out.WriteString(v[start:i])
		switch c {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteByte(c)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			out.WriteString(`\u00`)
			out.WriteByte(hex[c>>4])
			out.WriteByte(hex[c&0xf])
		}
		start = i + 1
	}
	out.WriteString(v[start:])
	out.WriteByte('"')
	return out.String()
}
