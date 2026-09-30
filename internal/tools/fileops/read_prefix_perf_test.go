package fileops

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkReadPrefixSkipping(b *testing.B) {
	for _, tc := range []struct {
		name               string
		lines, width, from int
	}{
		{"small", 300, 80, 1},
		{"late_short", 100000, 80, 99981},
		{"late_wide", 100000, 160, 99981},
		{"late_tiny", 100000, 1, 99981},
		{"huge_line", 3, 1024 * 1024, 3},
	} {
		body := strings.Repeat(strings.Repeat("x", tc.width-1)+"\n", tc.lines)
		for _, implementation := range []struct {
			name string
			read func(context.Context, io.Reader, string, int, int, int, []LineSpan, *bool) ([]LineRange, int, error)
		}{{"reference", readLineWindowsReference}, {"current", readLineWindowsEOF}} {
			b.Run(tc.name+"/"+implementation.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					var eof bool
					out, completed, err := implementation.read(context.Background(), strings.NewReader(body), "source.go", tc.from, min(tc.lines, tc.from+19), 1800, nil, &eof)
					if err != nil || len(out) != min(20, tc.lines-tc.from+1) || completed != min(tc.lines, tc.from+19) {
						b.Fatalf("bad range: %d %d %v", len(out), completed, err)
					}
				}
			})
		}
	}
}

// Previous scanner retained only as a test oracle and paired benchmark baseline.
func readLineWindowsReference(ctx context.Context, input io.Reader, path string, from, to, maxLineBytes int, windows []LineSpan, eof *bool) ([]LineRange, int, error) {
	r := bufio.NewReaderSize(input, 32*1024)
	head, err := r.Peek(binarySniffBytes + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, err
	}
	if err := ensureTextBytes(path, head, len(head) > binarySniffBytes); err != nil {
		return nil, 0, err
	}
	utf8Text := classify(head[:min(len(head), binarySniffBytes)], len(head) > binarySniffBytes) == verdictText
	if len(windows) == 0 {
		return consumeLineWindowReference(ctx, r, from, to, maxLineBytes, utf8Text, eof)
	}
	var out []LineRange
	completed := 0
	for _, window := range windows {
		// Continue from the existing buffer, including prefetched bytes.
		// The hot scan loop stays identical for single and multiple ranges.
		lines, count, err := consumeLineWindowReference(ctx, r, window.From-completed, window.To-completed, maxLineBytes, utf8Text, eof)
		for i := range lines {
			lines[i].Number += completed
		}
		completed += count
		out = append(out, lines...)
		if err != nil || completed < window.To || (eof != nil && *eof) {
			return out, completed, err
		}
	}
	return out, completed, nil
}

func consumeLineWindowReference(ctx context.Context, r *bufio.Reader, from, to, maxLineBytes int, utf8Text bool, eof *bool) ([]LineRange, int, error) {
	var out []LineRange
	var content []byte
	lineNo, completed, lineBytes := 1, 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return out, completed, err
		}
		fragment, readErr := r.ReadSlice('\n')
		if readErr != nil && !errors.Is(readErr, bufio.ErrBufferFull) && !errors.Is(readErr, io.EOF) {
			return out, completed, readErr
		}
		if len(fragment) > 0 && fragment[len(fragment)-1] == '\n' {
			fragment = fragment[:len(fragment)-1]
		}
		lineBytes += len(fragment)
		if lineNo >= from {
			keep := len(fragment)
			if maxLineBytes > 0 {
				keep = min(keep, max(0, maxLineBytes-len(content)))
			}
			content = append(content, fragment[:keep]...)
		}
		// Pending bytes matter when EOF falls exactly on a buffer boundary.
		if !errors.Is(readErr, bufio.ErrBufferFull) && (lineBytes > 0 || readErr == nil) {
			if lineNo >= from {
				if lineBytes > len(content) && utf8Text {
					content = trimPartialRune(content)
				}
				text := string(content)
				if omitted := lineBytes - len(content); omitted > 0 {
					text += fmt.Sprintf(" …[+%d bytes on this line truncated; use ctx_execute for the full line]", omitted)
				}
				out = append(out, LineRange{Number: lineNo, Content: text})
			}
			completed = lineNo
			if completed == to {
				if eof != nil {
					// Usually answered from the existing buffer. At its boundary,
					// one bounded refill distinguishes exact EOF from another line.
					if errors.Is(readErr, io.EOF) {
						*eof = true
					} else {
						_, nextErr := r.Peek(1)
						*eof = errors.Is(nextErr, io.EOF)
					}
				}
				return out, completed, nil
			}
			lineNo++
			lineBytes = 0
			content = content[:0]
		}
		if errors.Is(readErr, io.EOF) {
			if eof != nil {
				*eof = true
			}
			return out, completed, nil
		}
	}
}

func BenchmarkReadPrefixFile(b *testing.B) {
	for _, width := range []int{80, 160} {
		path := filepath.Join(b.TempDir(), "large.go")
		body := strings.Repeat(strings.Repeat("x", width-1)+"\n", 100000)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			b.Fatal(err)
		}
		for _, implementation := range []struct {
			name string
			read func(context.Context, io.Reader, string, int, int, int, []LineSpan, *bool) ([]LineRange, int, error)
		}{{"reference", readLineWindowsReference}, {"current", readLineWindowsEOF}} {
			b.Run(fmt.Sprintf("width_%d/%s", width, implementation.name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					f, err := os.Open(path)
					if err != nil {
						b.Fatal(err)
					}
					var eof bool
					out, completed, err := implementation.read(context.Background(), f, path, 99981, 100000, 1800, nil, &eof)
					f.Close()
					if err != nil || len(out) != 20 || completed != 100000 || !eof {
						b.Fatalf("bad disk range: %d %d %v eof=%v", len(out), completed, err, eof)
					}
				}
			})
		}
	}
}
