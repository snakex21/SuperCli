package fileops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStreamingRangesMatchWholeFile(t *testing.T) {
	cases := []string{"", "\n", "\n\n", "a", "a\n", "a\r\nb\r\n\r\nend", "zażółć\n日本語\n🙂", "legacy-\xb9\xea\nend", strings.Repeat("x", 32768), strings.Repeat("x", 65536) + "\nlast"}
	for i, content := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.txt")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			all, err := readLines(path)
			if err != nil {
				t.Fatal(err)
			}
			for from := 1; from <= len(all)+1; from++ {
				got, err := ReadLines(path, from, from+2)
				if from > len(all) {
					want := fmt.Sprintf("fileops.ReadLines: from=%d exceeds file length %d", from, len(all))
					if err == nil || err.Error() != want {
						t.Fatalf("got %v want %s", err, want)
					}
					continue
				}
				var want []LineRange
				for n := from; n <= min(from+2, len(all)); n++ {
					want = append(want, LineRange{n, all[n-1]})
				}
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("from %d: got %#v, %v; want %#v", from, got, err, want)
				}
			}
		})
	}
}

type observedReader struct {
	input  io.Reader
	bytes  int
	reads  int
	cancel context.CancelFunc
}

func (r *observedReader) Read(p []byte) (int, error) {
	n, err := r.input.Read(p)
	r.bytes += n
	r.reads++
	if r.cancel != nil && r.reads == 2 {
		r.cancel()
	}
	return n, err
}

func TestStreamingStopsAtRangeAndCancelsHugeSkippedLine(t *testing.T) {
	data := "first\nsecond\n" + strings.Repeat("x", 4*1024*1024)
	r := &observedReader{input: strings.NewReader(data)}
	got, _, err := readLineWindow(context.Background(), r, "source.txt", 1, 2, 1800)
	if err != nil || len(got) != 2 || got[1].Content != "second" {
		t.Fatalf("got %v, %v", got, err)
	}
	if r.bytes > 32768 {
		t.Fatalf("read %d bytes for two early lines", r.bytes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r = &observedReader{input: strings.NewReader(strings.Repeat("x", 4*1024*1024) + "\ntarget\n"), cancel: cancel}
	_, _, err = readLineWindow(ctx, r, "source.txt", 2, 2, 1800)
	if !errors.Is(err, context.Canceled) || r.bytes > 65536 {
		t.Fatalf("cancel ignored: %v, %d bytes", err, r.bytes)
	}
}

func TestStreamingBoundedHugeLineKeepsUTF8AndNextLine(t *testing.T) {
	for _, ending := range []string{"", "\nnext\n"} {
		content := strings.Repeat("🙂", 16384) + ending // exact two-buffer boundary
		got, _, err := readLineWindow(context.Background(), strings.NewReader(content), "source.txt", 1, 2, 1801)
		if err != nil || len(got) == 0 {
			t.Fatalf("%v %v", got, err)
		}
		if len(got[0].Content) > 1900 || !utf8.ValidString(got[0].Content) || !strings.Contains(got[0].Content, "+63736 bytes") {
			t.Fatalf("bad bounded line: %.100s (%d bytes)", got[0].Content, len(got[0].Content))
		}
		if ending != "" && (len(got) != 2 || got[1].Content != "next") {
			t.Fatal("lost line following huge line")
		}
	}
}

func TestStreamingPreservesBinaryGuardAndLegacyBytes(t *testing.T) {
	for _, data := range [][]byte{[]byte("plain\x00data\x00\x00"), {0xff, 0xfe, 'a', 0, '\n', 0}, []byte("legacy-\xb9\xea\n"), bytes.Repeat([]byte("ż"), 5000)} {
		want := ensureTextBytes("source.txt", data, false)
		got, _, err := readLineWindow(context.Background(), bytes.NewReader(data), "source.txt", 1, 2, 0)
		if fmt.Sprint(err) != fmt.Sprint(want) {
			t.Fatalf("guard changed: %v vs %v", err, want)
		}
		if want == nil && (len(got) != 1 || got[0].Content != strings.TrimSuffix(string(data), "\n")) {
			t.Fatal("text bytes changed")
		}
	}
}

func TestStreamingContextPastEOFAndOverflow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, line := range []int{3, math.MaxInt} {
		_, err := ReadContext(path, line, 250)
		want := fmt.Sprintf("fileops.ReadContext: line=%d exceeds file length 2", line)
		if err == nil || err.Error() != want {
			t.Fatalf("got %v want %s", err, want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ReadLinesBounded(ctx, path, 1, 2, 1800)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func BenchmarkReadRangeStreaming(b *testing.B) {
	path := filepath.Join(b.TempDir(), "large.go")
	if err := os.WriteFile(path, []byte(strings.Repeat(strings.Repeat("x", 159)+"\n", 100000)), 0600); err != nil {
		b.Fatal(err)
	}
	for _, at := range []struct {
		name string
		from int
	}{{"early", 1}, {"middle", 50000}, {"late", 99981}} {
		b.Run(at.name+"/whole_file_baseline", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				all, err := readLines(path)
				if err != nil {
					b.Fatal(err)
				}
				out := make([]LineRange, 0, 20)
				for n := at.from; n < at.from+20; n++ {
					out = append(out, LineRange{n, all[n-1]})
				}
				if len(out) != 20 {
					b.Fatal("bad baseline")
				}
			}
		})
		b.Run(at.name+"/stream", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				out, err := ReadLines(path, at.from, at.from+19)
				if err != nil || len(out) != 20 {
					b.Fatalf("%v %v", out, err)
				}
			}
		})
	}
}
