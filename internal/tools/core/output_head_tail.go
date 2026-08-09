package core

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

// lineCutSlack bounds how far a byte cut may move to reach a line
// boundary. Cuts are allowed to travel up to this many bytes so the
// head/tail never split a line, but a pathological single-line output
// still caps at the byte budget.
const lineCutSlack = 1024

// HeadTail caps s to roughly head+tail bytes by keeping the first
// `head` and last `tail` bytes with an explicit omission marker in
// between:
//
//	<head bytes>
//	[... omitted_bytes=N, omitted_lines=M ...]
//	<tail bytes>
//
// Both cuts land on UTF-8 rune boundaries (the head cut moves back,
// the tail cut moves forward, at most 3 bytes each) so a multi-byte
// character is never split. When a newline is within lineCutSlack of a
// cut, the cut moves to the line boundary instead, so head and tail
// always start on complete lines; M counts the lines lost between the
// cuts. Strings that already fit are returned unchanged. Same
// convention as the c74e100 read caps.
func HeadTail(s string, head, tail int) string {
	if len(s) <= head+tail {
		return s
	}
	h := head
	for h > 0 && !utf8.RuneStart(s[h]) {
		h--
	}
	i := len(s) - tail
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	// Move the cuts to line boundaries when a newline is close enough;
	// h only moves back and i only moves forward, so the gap between
	// them cannot close.
	if nl := strings.LastIndexByte(s[:h], '\n'); nl >= 0 && h-(nl+1) <= lineCutSlack {
		h = nl + 1
	}
	if nl := strings.IndexByte(s[i:], '\n'); nl >= 0 && nl <= lineCutSlack {
		i += nl + 1
	}
	omitted := int64(i - h)
	lines := int64(bytes.Count([]byte(s[h:i]), []byte{'\n'}))
	return s[:h] + omissionMarker(omitted, lines) + s[i:]
}

func omissionMarker(omittedBytes, omittedLines int64) string {
	return fmt.Sprintf("\n[... omitted_bytes=%d, omitted_lines=%d ...]\n", omittedBytes, omittedLines)
}

// HeadTailBuffer is an io.Writer that keeps only the first HeadMax
// and last TailMax bytes ever written, in bounded memory, counting
// what it drops (bytes and complete lines). It exists so a subprocess
// that prints 500 MB does not cost 500 MB of RAM before the result is
// truncated — the cap is enforced DURING the run, not after (unlike
// CombinedOutput).
//
// Writes are serialized with a mutex, so one buffer can safely be
// shared as both cmd.Stdout and cmd.Stderr (combined-output shape).
type HeadTailBuffer struct {
	mu           sync.Mutex
	headMax      int
	tailMax      int
	head         []byte
	tail         []byte // rolling window; trimmed when it exceeds 2*tailMax
	omitted      int64
	omittedLines int64 // '\n' seen in dropped bytes
}

// NewHeadTailBuffer returns a buffer keeping the first headMax and
// last tailMax bytes. Both must be > 0.
func NewHeadTailBuffer(headMax, tailMax int) *HeadTailBuffer {
	return &HeadTailBuffer{headMax: headMax, tailMax: tailMax}
}

// Write implements io.Writer. It never fails and always reports the
// full length as written.
func (b *HeadTailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if len(b.head) < b.headMax {
		take := b.headMax - len(b.head)
		if take > len(p) {
			take = len(p)
		}
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	switch {
	case len(p) == 0:
	case len(p) >= b.tailMax:
		// The chunk alone fills the tail window: everything
		// buffered so far and the chunk's own prefix are dropped.
		drop := len(b.tail) + len(p) - b.tailMax
		b.omitted += int64(drop)
		b.omittedLines += int64(bytes.Count(b.tail, newline)) + int64(bytes.Count(p[:len(p)-b.tailMax], newline))
		b.tail = append(b.tail[:0], p[len(p)-b.tailMax:]...)
	default:
		b.tail = append(b.tail, p...)
		if len(b.tail) > 2*b.tailMax {
			cut := len(b.tail) - b.tailMax
			b.omitted += int64(cut)
			b.omittedLines += int64(bytes.Count(b.tail[:cut], newline))
			copy(b.tail, b.tail[cut:])
			b.tail = b.tail[:b.tailMax]
		}
	}
	return n, nil
}

// newline is the byte pattern counted for omitted_lines.
var newline = []byte{'\n'}

// Truncated reports whether any bytes were (or will be) dropped.
func (b *HeadTailBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.omitted > 0 || len(b.tail) > b.tailMax
}

// String renders the captured output. When nothing was dropped the
// result is the exact byte stream; otherwise head + omission marker
// + tail, with both cuts moved to UTF-8 rune boundaries and, when a
// newline is within lineCutSlack, to line boundaries so the head and
// tail start on complete lines.
func (b *HeadTailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	tail := b.tail
	omitted := b.omitted
	omittedLines := b.omittedLines
	if len(tail) > b.tailMax {
		cut := len(tail) - b.tailMax
		omitted += int64(cut)
		omittedLines += int64(bytes.Count(tail[:cut], newline))
		tail = tail[cut:]
	}
	if omitted == 0 && omittedLines == 0 {
		return string(b.head) + string(tail)
	}
	head := trimIncompleteRune(b.head)
	omitted += int64(len(b.head) - len(head))
	// The partial rune adjustment cannot cross a '\n' (a newline is a
	// full ASCII rune), so no line accounting is needed for it.
	if nl := bytes.LastIndexByte(head, '\n'); nl >= 0 && len(head)-(nl+1) <= lineCutSlack {
		omitted += int64(len(head) - nl - 1)
		head = head[:nl+1]
	}
	i := 0
	for i < len(tail) && !utf8.RuneStart(tail[i]) {
		i++
	}
	omitted += int64(i)
	if nl := bytes.IndexByte(tail[i:], '\n'); nl >= 0 && nl <= lineCutSlack {
		// Drop the partial line and its newline so the kept tail
		// starts exactly at a line boundary.
		omitted += int64(nl + 1)
		omittedLines++
		i += nl + 1
	}
	return string(head) + omissionMarker(omitted, omittedLines) + string(tail[i:])
}

// trimIncompleteRune drops a trailing incomplete UTF-8 sequence
// (at most 3 bytes) so the head never ends mid-character.
func trimIncompleteRune(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return b[:i]
			}
			break
		}
	}
	return b
}
