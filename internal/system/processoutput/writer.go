// Package processoutput converts child text streams before bounded capture.
// UTF-8 passes through; BOM-marked UTF-16 and legacy Windows console output
// are decoded without changing the command, locale, or numeric formatting.
package processoutput

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type Writer struct {
	dst          io.Writer
	pending      []byte
	started      bool
	utf16        binary.ByteOrder
	legacy       bool
	decodeLegacy func([]byte) string
}

func NewWriter(dst io.Writer) *Writer {
	return &Writer{dst: dst, decodeLegacy: legacyText}
}

func (w *Writer) Write(p []byte) (int, error) {
	n := len(p)
	if len(w.pending) > 0 {
		p = append(w.pending, p...)
		w.pending = nil
	}
	return n, w.decode(p, false)
}

// Close flushes an incomplete final sequence; it does not close the destination.
func (w *Writer) Close() error {
	p := w.pending
	w.pending = nil
	return w.decode(p, true)
}

func (w *Writer) decode(p []byte, final bool) error {
	if len(p) == 0 {
		return nil
	}
	if !w.started {
		// A BOM can arrive byte by byte. ASCII streams need no detection wait.
		if !final && ((p[0] == 0xef && len(p) < 3) || ((p[0] == 0xff || p[0] == 0xfe) && len(p) < 2)) {
			w.pending = append([]byte(nil), p...)
			return nil
		}
		w.started = true
		switch {
		case bytes.HasPrefix(p, []byte{0xff, 0xfe}):
			w.utf16 = binary.LittleEndian
			p = p[2:]
		case bytes.HasPrefix(p, []byte{0xfe, 0xff}):
			w.utf16 = binary.BigEndian
			p = p[2:]
		case bytes.HasPrefix(p, []byte{0xef, 0xbb, 0xbf}):
			p = p[3:]
		}
	}
	var text string
	if w.utf16 != nil {
		end := len(p) - len(p)%2
		if !final && end >= 2 {
			r := w.utf16.Uint16(p[end-2 : end])
			if r >= 0xd800 && r <= 0xdbff {
				end -= 2
			}
		}
		units := make([]uint16, end/2)
		for i := range units {
			units[i] = w.utf16.Uint16(p[2*i:])
		}
		text = string(utf16.Decode(units))
		if final && len(p)%2 != 0 {
			text += "\uFFFD"
		}
		if !final {
			w.pending = append([]byte(nil), p[end:]...)
		}
	} else if w.legacy {
		text = w.decodeLegacy(p)
	} else {
		if utf8.Valid(p) {
			_, err := w.dst.Write(p)
			return err
		}
		end := len(p)
		for i := 0; i < len(p); {
			if !utf8.FullRune(p[i:]) && !final {
				end = i
				break
			}
			r, size := utf8.DecodeRune(p[i:])
			if r == utf8.RuneError && size == 1 {
				// Preserve any preceding valid UTF-8; only the legacy remainder
				// switches code pages. This also handles ASCII then OEM output.
				text = string(p[:i]) + w.decodeLegacy(p[i:])
				w.legacy = true
				_, err := io.WriteString(w.dst, text)
				return err
			}
			i += size
		}
		if end < len(p) {
			w.pending = append([]byte(nil), p[end:]...)
		}
		_, err := w.dst.Write(p[:end])
		return err
	}
	_, err := io.WriteString(w.dst, text)
	return err
}

func validText(p []byte) string { return strings.ToValidUTF8(string(p), "\uFFFD") }
