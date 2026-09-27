package fileops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGroupedReadEOFMatchesIndependentRanges(t *testing.T) {
	spans := []LineSpan{{7, 20}, {1, 1}, {2, 5}, {1, 5}, {2, 2}, {50, 52}, {0, 1}, {3, 2}, {1, MaxLineRange + 1}, {80, 80}, {79, 90}}
	for n, body := range []string{
		"", "\n", "a\r\nb\r\n\r\nend", "żółw\n日本語\n🙂", "legacy-\xb9\xea\nend",
		strings.Repeat("line\n", 80), "first\n" + strings.Repeat("🙂", 32768) + "\nlast\n", "plain\x00binary\x00",
	} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.txt")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			for _, limit := range []int{0, 33, 1801} {
				got := ReadRangesBoundedWithEOF(context.Background(), path, spans, limit)
				for i, span := range spans {
					lines, eof, err := ReadLinesBoundedWithEOF(context.Background(), path, span.From, span.To, limit)
					if !reflect.DeepEqual(got[i].Lines, lines) || got[i].EOF != eof || fmt.Sprint(got[i].Err) != fmt.Sprint(err) {
						t.Fatalf("span=%+v limit=%d: got=%+v want=%+v EOF=%t error=%v", span, limit, got[i], lines, eof, err)
					}
				}
			}
		})
	}
}

func TestGroupedReadEOFStopsAtLastRequestedWindow(t *testing.T) {
	body := strings.Repeat("line\n", 100000)
	r := &observedReader{input: strings.NewReader(body)}
	var eof bool
	_, completed, err := readLineWindowsEOF(context.Background(), r, "source.txt", 1, 500, 80, []LineSpan{{1, 2}, {499, 500}}, &eof)
	if err != nil || completed != 500 || eof || r.bytes > 32768 {
		t.Fatalf("unnecessary tail read: completed=%d eof=%t bytes=%d err=%v", completed, eof, r.bytes, err)
	}
}

func TestGroupedReadConsumesRepeatedPrefixOnce(t *testing.T) {
	body := strings.Repeat("fixed source content for a numbered line\n", 42000)
	spans := []LineSpan{{40001, 40300}, {40201, 40500}, {40401, 40700}, {40601, 40900}}
	independentBytes := 0
	for _, span := range spans {
		r := &observedReader{input: strings.NewReader(body)}
		var eof bool
		lines, _, err := readLineWindowsEOF(context.Background(), r, "source.txt", span.From, span.To, 1800, nil, &eof)
		if err != nil || len(lines) != 300 || eof {
			t.Fatalf("independent read: lines=%d EOF=%t err=%v", len(lines), eof, err)
		}
		independentBytes += r.bytes
	}
	merged := []LineSpan{{40001, 40900}}
	r := &observedReader{input: strings.NewReader(body)}
	var eof bool
	lines, _, err := readLineWindowsEOF(context.Background(), r, "source.txt", 40001, 40900, 1800, merged, &eof)
	if err != nil || len(lines) != 900 || eof || r.bytes >= independentBytes/3 {
		t.Fatalf("group did not remove repeated scanning: lines=%d bytes=%d prior=%d err=%v", len(lines), r.bytes, independentBytes, err)
	}
	t.Logf("input reader bytes: independent=%d grouped=%d", independentBytes, r.bytes)
}
