package fileops

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// ReadLines reads an inclusive, 1-based range, capped at MaxLineRange.
// Only the requested lines are retained; reading stops at the range's end.
func ReadLines(path string, from, to int) ([]LineRange, error) {
	return ReadLinesBounded(context.Background(), path, from, to, 0)
}

// ReadLinesBounded also supports cancellation and a per-line byte limit.
// A zero limit preserves complete lines; positive limits add omission markers.
func ReadLinesBounded(ctx context.Context, path string, from, to, maxLineBytes int) ([]LineRange, error) {
	return readLinesBounded(ctx, path, from, to, maxLineBytes, nil)
}

// ReadLinesBoundedWithEOF reports EOF observed by this read, without counting
// the rest of the file. False means that more data may follow the range.
func ReadLinesBoundedWithEOF(ctx context.Context, path string, from, to, maxLineBytes int) ([]LineRange, bool, error) {
	var eof bool
	out, err := readLinesBounded(ctx, path, from, to, maxLineBytes, &eof)
	return out, eof, err
}

func readLinesBounded(ctx context.Context, path string, from, to, maxLineBytes int, eof *bool) ([]LineRange, error) {
	if err := validateLineRange(from, to); err != nil {
		return nil, err
	}
	out, completed, err := readWindow(ctx, path, from, to, maxLineBytes, eof)
	if err != nil {
		return nil, err
	}
	if from > completed {
		return nil, fmt.Errorf("fileops.ReadLines: from=%d exceeds file length %d", from, completed)
	}
	return out, nil
}

// ReadContext reads radius lines around line (1-based).
// DefaultContextRadius is used when radius <= 0.
func ReadContext(path string, line, radius int) ([]LineRange, error) {
	return ReadContextBounded(context.Background(), path, line, radius, 0)
}

// ReadContextBounded applies the same bounds and cancellation as ReadLinesBounded.
func ReadContextBounded(ctx context.Context, path string, line, radius, maxLineBytes int) ([]LineRange, error) {
	return readContextBounded(ctx, path, line, radius, maxLineBytes, nil)
}

// ReadContextBoundedWithEOF is ReadContextBounded with observed EOF metadata.
func ReadContextBoundedWithEOF(ctx context.Context, path string, line, radius, maxLineBytes int) ([]LineRange, bool, error) {
	var eof bool
	out, err := readContextBounded(ctx, path, line, radius, maxLineBytes, &eof)
	return out, eof, err
}

func readContextBounded(ctx context.Context, path string, line, radius, maxLineBytes int, eof *bool) ([]LineRange, error) {
	if line < 1 {
		return nil, fmt.Errorf("fileops.ReadContext: line=%d must be >= 1", line)
	}
	if radius <= 0 {
		radius = DefaultContextRadius
	}
	if radius > MaxContextRadius {
		radius = MaxContextRadius
	}
	from := max(1, line-radius)
	to := math.MaxInt
	if line <= math.MaxInt-radius {
		to = line + radius
	}
	out, completed, err := readWindow(ctx, path, from, to, maxLineBytes, eof)
	if err != nil {
		return nil, err
	}
	if line > completed {
		return nil, fmt.Errorf("fileops.ReadContext: line=%d exceeds file length %d", line, completed)
	}
	return out, nil
}

func readWindow(ctx context.Context, path string, from, to, maxLineBytes int, eof *bool) ([]LineRange, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, FileErr(err, path)
	}
	defer f.Close()
	out, completed, err := readLineWindowsEOF(ctx, f, path, from, to, maxLineBytes, nil, eof)
	if err != nil {
		return nil, completed, FileErr(err, path)
	}
	return out, completed, nil
}

// ReadSlice consumes huge or skipped lines in fixed-size chunks. The head
// check uses this same buffer, preserving encoding checks without reopening
// the file or allocating its entire contents. CR is preserved, like readLines.
func readLineWindow(ctx context.Context, input io.Reader, path string, from, to, maxLineBytes int) ([]LineRange, int, error) {
	out, completed, err := readLineWindows(ctx, input, path, from, to, maxLineBytes, nil)
	if err != nil {
		return nil, completed, err
	}
	return out, completed, nil
}

// windows is either nil (one continuous range) or sorted, disjoint spans.
func readLineWindows(ctx context.Context, input io.Reader, path string, from, to, maxLineBytes int, windows []LineSpan) ([]LineRange, int, error) {
	return readLineWindowsEOF(ctx, input, path, from, to, maxLineBytes, windows, nil)
}

func readLineWindowsEOF(ctx context.Context, input io.Reader, path string, from, to, maxLineBytes int, windows []LineSpan, eof *bool) ([]LineRange, int, error) {
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
		return consumeLineWindow(ctx, r, from, to, maxLineBytes, utf8Text, eof)
	}
	var out []LineRange
	completed := 0
	for _, window := range windows {
		// Continue from the existing buffer, including prefetched bytes.
		// The hot scan loop stays identical for single and multiple ranges.
		lines, count, err := consumeLineWindow(ctx, r, window.From-completed, window.To-completed, maxLineBytes, utf8Text, nil)
		for i := range lines {
			lines[i].Number += completed
		}
		completed += count
		out = append(out, lines...)
		if err != nil || completed < window.To {
			return out, completed, err
		}
	}
	return out, completed, nil
}

func consumeLineWindow(ctx context.Context, r *bufio.Reader, from, to, maxLineBytes int, utf8Text bool, eof *bool) ([]LineRange, int, error) {
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

func validateLineRange(from, to int) error {
	if from < 1 {
		return fmt.Errorf("fileops.ReadLines: from=%d must be >= 1", from)
	}
	if to < from {
		return fmt.Errorf("fileops.ReadLines: to=%d must be >= from=%d; lines are 1-based", to, from)
	}
	if to-from >= MaxLineRange {
		return fmt.Errorf("fileops.ReadLines: range %d lines exceeds cap %d", to-from+1, MaxLineRange)
	}
	return nil
}
