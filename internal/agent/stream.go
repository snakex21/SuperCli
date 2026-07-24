package agent

import (
	"strings"
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
