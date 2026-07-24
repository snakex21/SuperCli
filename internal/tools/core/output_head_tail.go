package core

import (
	"fmt"
	"sync"
	"unicode/utf8"
)

// HeadTail caps s to roughly head+tail bytes by keeping the first
// `head` and last `tail` bytes with an explicit omission marker in
// between:
//
//	<head bytes>
//	[... omitted_bytes=N ...]
//	<tail bytes>
//
// Both cuts land on UTF-8 rune boundaries (the head cut moves back,
// the tail cut moves forward, at most 3 bytes each) so a multi-byte
// character is never split. Strings that already fit are returned
// unchanged. Same convention as the c74e100 read caps.
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
	return s[:h] + omissionMarker(int64(i-h)) + s[i:]
}

func omissionMarker(n int64) string {
	return fmt.Sprintf("\n[... omitted_bytes=%d ...]\n", n)
}

// HeadTailBuffer is an io.Writer that keeps only the first HeadMax
// and last TailMax bytes ever written, in bounded memory, counting
// what it drops. It exists so a subprocess that prints 500 MB does
// not cost 500 MB of RAM before the result is truncated — the cap
// is enforced DURING the run, not after (unlike CombinedOutput).
//
// Writes are serialized with a mutex, so one buffer can safely be
// shared as both cmd.Stdout and cmd.Stderr (combined-output shape).
type HeadTailBuffer struct {
	mu      sync.Mutex
	headMax int
	tailMax int
	head    []byte
	tail    []byte // rolling window; trimmed when it exceeds 2*tailMax
	omitted int64
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
		b.omitted += int64(len(b.tail)) + int64(len(p)-b.tailMax)
		b.tail = append(b.tail[:0], p[len(p)-b.tailMax:]...)
	default:
		b.tail = append(b.tail, p...)
		if len(b.tail) > 2*b.tailMax {
			cut := len(b.tail) - b.tailMax
			b.omitted += int64(cut)
			copy(b.tail, b.tail[cut:])
			b.tail = b.tail[:b.tailMax]
		}
	}
	return n, nil
}

// Truncated reports whether any bytes were (or will be) dropped.
func (b *HeadTailBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.omitted > 0 || len(b.tail) > b.tailMax
}

// String renders the captured output. When nothing was dropped the
// result is the exact byte stream; otherwise head + omission marker
// + tail, with both cuts moved to UTF-8 rune boundaries.
func (b *HeadTailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	tail := b.tail
	omitted := b.omitted
	if len(tail) > b.tailMax {
		cut := len(tail) - b.tailMax
		omitted += int64(cut)
		tail = tail[cut:]
	}
	if omitted == 0 {
		return string(b.head) + string(tail)
	}
	head := trimIncompleteRune(b.head)
	omitted += int64(len(b.head) - len(head))
	i := 0
	for i < len(tail) && !utf8.RuneStart(tail[i]) {
		i++
	}
	omitted += int64(i)
	return string(head) + omissionMarker(omitted) + string(tail[i:])
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
