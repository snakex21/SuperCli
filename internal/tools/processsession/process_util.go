// Package processsession provides bounded, workspace-scoped long-running
// command sessions. It complements ctx_execute: short commands remain cheaper
// there, while servers, watchers and interactive programs can be started once
// and polled without blocking an agent turn.
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

type streamBuffer struct {
	mu   sync.Mutex
	max  int
	base int64
	data []byte
}

func newStreamBuffer(max int) *streamBuffer { return &streamBuffer{max: max} }

func (b *streamBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	b.data = append(b.data, p...)
	if len(b.data) > b.max {
		drop := len(b.data) - b.max
		b.data = append([]byte(nil), b.data[drop:]...)
		b.base += int64(drop)
	}
	return n, nil
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
	data := b.data[start:]
	if len(data) > max {
		data = data[:max]
	}
	out := append([]byte(nil), data...)
	return out, cursor + int64(len(data)), omitted
}
