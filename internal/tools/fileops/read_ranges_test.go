package fileops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadRangesMatchIndependentReads(t *testing.T) {
	spans := []LineSpan{{7, 20}, {1, 1}, {2, 5}, {1, 5}, {2, 2}, {50, 52}, {0, 1}, {3, 2}, {1, MaxLineRange + 1}}
	contents := []string{"", "\n", "a\r\nb\r\n\r\nend", "żółw\n日本語\n🙂", "legacy-\xb9\xea\nend",
		strings.Repeat("line\n", 80), "first\n" + strings.Repeat("🙂", 32768) + "\nlast\n", "plain\x00binary\x00"}
	for i, body := range contents {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.txt")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			for _, limit := range []int{0, 33, 1801} {
				results := ReadRangesBounded(context.Background(), path, spans, limit)
				for j, span := range spans {
					want, wantErr := ReadLinesBounded(context.Background(), path, span.From, span.To, limit)
					if !reflect.DeepEqual(results[j].Lines, want) || fmt.Sprint(results[j].Err) != fmt.Sprint(wantErr) {
						t.Fatalf("span=%+v limit=%d got=%+v want=%+v error=%v", span, limit, results[j], want, wantErr)
					}
				}
			}
		})
	}
}

func TestReadRangesErrorsAndFreshness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.txt")
	spans := []LineSpan{{1, 2}, {1, 1}}
	for _, result := range ReadRangesBounded(context.Background(), path, spans, 80) {
		if !errors.Is(result.Err, os.ErrNotExist) {
			t.Fatalf("missing: %+v", result)
		}
	}
	if err := os.WriteFile(path, []byte("old\nsecond\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, result := range ReadRangesBounded(ctx, path, spans, 80) {
		if !errors.Is(result.Err, context.Canceled) {
			t.Fatalf("cancel: %+v", result)
		}
	}
	old := ReadRangesBounded(context.Background(), path, spans, 80)
	old[0].Lines[0].Content = "caller mutation"
	if old[1].Lines[0].Content != "old" {
		t.Fatal("overlapping slices alias")
	}
	if err := os.WriteFile(path, []byte("new\nsecond\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fresh := ReadRangesBounded(context.Background(), path, spans, 80)
	if fresh[0].Lines[0].Content != "new" {
		t.Fatal("stale content reused across calls")
	}
}

func TestReadWindowsSkipsGapsStopsAndCancels(t *testing.T) {
	// Eight MiB between selected lines must be consumed, but never retained.
	body := "first\n" + strings.Repeat("x", 8*1024*1024) + "\nthird\nlast\n" + strings.Repeat("tail\n", 100000)
	r := &observedReader{input: strings.NewReader(body)}
	spans := []LineSpan{{1, 1}, {4, 4}}
	lines, completed, err := readLineWindows(context.Background(), r, "source.txt", 1, 4, 1801, spans)
	if err != nil || completed != 4 || !reflect.DeepEqual(lines, []LineRange{{1, "first"}, {4, "last"}}) {
		t.Fatalf("lines=%+v completed=%d error=%v", lines, completed, err)
	}
	if r.bytes > strings.Index(body, "last\n")+len("last\n")+32768 {
		t.Fatalf("read past final range: %d", r.bytes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r = &observedReader{input: strings.NewReader(body), cancel: cancel}
	lines, completed, err = readLineWindows(ctx, r, "source.txt", 1, 4, 80, spans)
	if !errors.Is(err, context.Canceled) || r.bytes > 65536 || completed != 1 || len(lines) != 1 {
		t.Fatalf("cancellation failed: bytes=%d completed=%d err=%v", r.bytes, completed, err)
	}
}

type failingRangeReader struct{ left int }

func (r *failingRangeReader) Read(b []byte) (int, error) {
	if r.left == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	n := min(len(b), r.left)
	for i := range b[:n] {
		b[i] = '\n'
	}
	r.left -= n
	return n, nil
}

func TestReadWindowsPreservesCompletedRangesOnIOError(t *testing.T) {
	lines, completed, err := readLineWindows(context.Background(), &failingRangeReader{left: 32768},
		"source.txt", 1, 50000, 80, []LineSpan{{1, 2}, {49999, 50000}})
	if !errors.Is(err, io.ErrUnexpectedEOF) || completed != 32768 || len(lines) != 2 {
		t.Fatalf("lines=%+v completed=%d error=%v", lines, completed, err)
	}
}
