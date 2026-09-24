package fileops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadWindowEOFBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		to         int
		eof        bool
	}{
		{"short", "a\nb\n", 20, true},
		{"exact-newline", "a\nb\n", 2, true},
		{"exact-no-newline", "a\nb", 2, true},
		{"crlf", "a\r\nb\r\n", 2, true},
		{"blank-last", "a\n\n", 2, true},
		{"next-blank", "a\n\n", 1, false},
		{"next-huge", "a\n" + strings.Repeat("z", 1<<20), 1, false},
		{"buffer-end", strings.Repeat("x", 32767) + "\n", 1, true},
		{"buffer-more", strings.Repeat("x", 32767) + "\n" + strings.Repeat("z", 1<<20), 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &observedReader{input: strings.NewReader(tc.text)}
			eof := false
			lines, _, err := readLineWindowsEOF(context.Background(), r, "text.txt", 1, tc.to, 1800, nil, &eof)
			if err != nil || len(lines) == 0 || eof != tc.eof {
				t.Fatalf("lines=%d eof=%v err=%v", len(lines), eof, err)
			}
			if r.bytes > 65536 {
				t.Fatalf("EOF detection scanned %d bytes", r.bytes)
			}
		})
	}
}

func TestReadWindowEOFErrorsAndContext(t *testing.T) {
	p := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(p, []byte("one\ntwo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	lines, eof, err := ReadContextBoundedWithEOF(context.Background(), p, 1, 1, 1800)
	if err != nil || len(lines) != 2 || !eof {
		t.Fatalf("%v %v %v", lines, eof, err)
	}
	for _, from := range []int{0, 3} {
		_, _, got := ReadLinesBoundedWithEOF(context.Background(), p, from, from+1, 1800)
		_, want := ReadLinesBounded(context.Background(), p, from, from+1, 1800)
		if got == nil || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("error changed: %v / %v", got, want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, eof, err = ReadLinesBoundedWithEOF(ctx, p, 1, 2, 1800)
	if !errors.Is(err, context.Canceled) || eof {
		t.Fatalf("cancel: %v %v", eof, err)
	}
}
