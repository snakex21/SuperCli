// Package processsession provides bounded, workspace-scoped long-running
// command sessions. It complements ctx_execute: short commands remain cheaper
// there, while servers, watchers and interactive programs can be started once
// and awaited on completion or inspected without restarting them.
package processsession

import (
	"fmt"
	"strings"
	"sync"
)

func terminalSize(columns, rows int) (int, int) {
	if columns < 20 || columns > 500 {
		columns = 100
	}
	if rows < 5 || rows > 200 {
		rows = 30
	}
	return columns, rows
}

// stripTerminalControl keeps terminal UX bytes out of the model context. It
// removes CSI/OSC escape sequences and normalizes carriage returns while
// preserving ordinary UTF-8 output.
func stripTerminalControl(src []byte) []byte {
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); {
		switch src[i] {
		case '\r':
			if i+1 < len(src) && src[i+1] == '\n' {
				i++
			}
			out = append(out, '\n')
			i++
		case 0x1b:
			i++
			if i >= len(src) {
				continue
			}
			switch src[i] {
			case '[':
				i++
				for i < len(src) {
					b := src[i]
					i++
					if b >= 0x40 && b <= 0x7e {
						break
					}
				}
			case ']':
				i++
				for i < len(src) {
					if src[i] == 0x07 {
						i++
						break
					}
					if src[i] == 0x1b && i+1 < len(src) && src[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
			default:
				i++
			}
		default:
			out = append(out, src[i])
			i++
		}
	}
	return out
}

func validatedEnv(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	for i, value := range values {
		key, _, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\x00\r\n") || strings.ContainsAny(value, "\x00\r\n") {
			return nil, fmt.Errorf("process_session: env[%d] must be KEY=VALUE", i)
		}
		out = append(out, value)
	}
	return out, nil
}

// streamBuffer retains the newest max bytes without reallocating or moving the
// entire retained tail for every chunk of a noisy long-running command. Storage
// grows lazily, so quiet sessions do not pay for an empty 64 KiB buffer.
type streamBuffer struct {
	mu   sync.Mutex
	max  int
	base int64
	data []byte
	head int
}

func newStreamBuffer(max int) *streamBuffer {
	if max < 0 {
		max = 0
	}
	return &streamBuffer{max: max}
}

func (b *streamBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	if b.max == 0 {
		b.base += int64(n)
		return n, nil
	}
	if n >= b.max {
		// Do not briefly retain an arbitrarily large incoming write before
		// trimming it. Only its bounded tail can survive.
		b.grow(b.max)
		b.base += int64(len(b.data)) + int64(n) - int64(b.max)
		b.data = b.data[:b.max]
		copy(b.data, p[n-b.max:])
		b.head = 0
		return n, nil
	}
	retained := len(b.data)
	if drop := retained + n - b.max; drop > 0 {
		b.head = (b.head + drop) % cap(b.data)
		b.base += int64(drop)
		retained -= drop
		b.data = b.data[:retained]
	}
	b.grow(retained + n)
	storage := b.data[:cap(b.data)]
	tail := (b.head + retained) % len(storage)
	copied := copy(storage[tail:], p)
	copy(storage, p[copied:])
	b.data = b.data[:retained+n]
	return n, nil
}

// grow preserves chronological order when a small buffer needs more space.
// Once the cap is reached, writes reuse the same allocation indefinitely.
func (b *streamBuffer) grow(needed int) {
	if needed <= cap(b.data) {
		return
	}
	capacity := cap(b.data) * 2
	if capacity < needed {
		capacity = needed
	}
	if capacity > b.max {
		capacity = b.max
	}
	data := make([]byte, len(b.data), capacity)
	if len(b.data) > 0 {
		storage := b.data[:cap(b.data)]
		copied := copy(data, storage[b.head:])
		copy(data[copied:], storage)
	}
	b.data = data
	b.head = 0
}

func (b *streamBuffer) readFrom(cursor int64, max int) ([]byte, int64, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	omitted := int64(0)
	if cursor < b.base {
		omitted = b.base - cursor
		cursor = b.base
	}
	end := b.base + int64(len(b.data))
	if cursor > end {
		cursor = end
	}
	start := int(cursor - b.base)
	count := len(b.data) - start
	if max < 0 {
		max = 0
	}
	if count > max {
		count = max
	}
	if count == 0 {
		return nil, cursor, omitted
	}
	// The caller owns its snapshot: later writes cannot alter returned bytes.
	out := make([]byte, count)
	storage := b.data[:cap(b.data)]
	start = (b.head + start) % len(storage)
	copied := copy(out, storage[start:])
	copy(out[copied:], storage)
	return out, cursor + int64(count), omitted
}
