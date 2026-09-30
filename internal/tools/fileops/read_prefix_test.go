package fileops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// Compare complete ranges with the previous scanner across refill boundaries,
// gaps, CRLF, legacy bytes, Unicode, empty lines and unterminated final lines.
func TestReadPrefixMatchesPreviousScanner(t *testing.T) {
	bodies := []string{"", "\n", "\n\n", "last", "last\n", strings.Repeat("x", 32768) + "\nlast", strings.Repeat("row\r\n", 12000) + "tail", strings.Repeat("🙂\r\n", 7000) + "tail", strings.Repeat("\n", 40000) + "target\n", strings.Repeat("legacy-\xb9\xea\n", 5000) + "end"}
	rng := rand.New(rand.NewSource(47))
	for i := 0; i < 50; i++ {
		var body strings.Builder
		for n := 0; n < 300; n++ {
			body.WriteString(strings.Repeat("ż", rng.Intn(120)))
			if rng.Intn(3) == 0 {
				body.WriteByte('\r')
			}
			if n < 299 || rng.Intn(2) == 0 {
				body.WriteByte('\n')
			}
		}
		bodies = append(bodies, body.String())
	}
	for i, body := range bodies {
		total := strings.Count(body, "\n")
		if body != "" && !strings.HasSuffix(body, "\n") {
			total++
		}
		for _, from := range []int{1, 2, max(1, total/2), max(1, total), total + 1, total + 2} {
			for _, chunk := range []int{31, 1023, 32768} {
				windows := []LineSpan{{From: from, To: from + 1}, {From: from + 4, To: from + 6}}
				var wantEOF, gotEOF bool
				want, wn, we := readLineWindowsReference(context.Background(), &prefixChunkReader{reader: strings.NewReader(body), chunk: chunk}, "source.txt", from, from+6, 71, windows, &wantEOF)
				got, gn, ge := readLineWindowsEOF(context.Background(), &prefixChunkReader{reader: strings.NewReader(body), chunk: chunk}, "source.txt", from, from+6, 71, windows, &gotEOF)
				if !reflect.DeepEqual(got, want) || gn != wn || fmt.Sprint(ge) != fmt.Sprint(we) || gotEOF != wantEOF {
					t.Fatalf("body %d from %d chunk %d: got %v %d %v eof=%v; want %v %d %v eof=%v", i, from, chunk, got, gn, ge, gotEOF, want, wn, we, wantEOF)
				}
			}
		}
	}
}

type prefixChunkReader struct {
	reader io.Reader
	chunk  int
}

func (r *prefixChunkReader) Read(p []byte) (int, error) {
	return r.reader.Read(p[:min(len(p), r.chunk)])
}

type prefixFailReader struct {
	data []byte
	err  error
}

func (r *prefixFailReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestReadPrefixPropagatesInputFailure(t *testing.T) {
	boom := errors.New("input failed")
	for _, body := range []string{strings.Repeat("ok\n", 15000) + "partial", strings.Repeat("x", 70000)} {
		var wantEOF, gotEOF bool
		want, wn, we := readLineWindowsReference(context.Background(), &prefixFailReader{data: []byte(body), err: boom}, "source.txt", 20000, 20001, 80, nil, &wantEOF)
		got, gn, ge := readLineWindowsEOF(context.Background(), &prefixFailReader{data: []byte(body), err: boom}, "source.txt", 20000, 20001, 80, nil, &gotEOF)
		if !errors.Is(ge, boom) || !reflect.DeepEqual(got, want) || gn != wn || !errors.Is(we, boom) || gotEOF != wantEOF {
			t.Fatalf("lost input failure: %v %d %v eof=%v", got, gn, ge, gotEOF)
		}
	}
}

func TestReadPrefixCancelsBeforeRequestedRange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &observedReader{input: bytes.NewReader(bytes.Repeat([]byte("skipped\n"), 100000)), cancel: cancel}
	var eof bool
	got, _, err := readLineWindowsEOF(ctx, reader, "source.txt", 90000, 90001, 80, nil, &eof)
	if !errors.Is(err, context.Canceled) || len(got) != 0 || eof || reader.bytes > 65536 {
		t.Fatalf("cancel ignored: %v %v eof=%v bytes=%d", got, err, eof, reader.bytes)
	}
}
