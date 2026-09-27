package core

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// Follow the actual tool footer instead of calculating offsets in the test.
// Every non-final page must advance, and every returned byte must be recoverable.
func walkBoundaryPages(t *testing.T, ctx context.Context, s *OutputStore, handle string, offset, limit int) string {
	t.Helper()
	reg := NewRegistry()
	reg.MustRegister(s.ReadOutputTool())
	nextRE := regexp.MustCompile("\\[next offset: ([0-9]+)\\]")
	var out strings.Builder
	for pages := 0; pages < 64; pages++ {
		raw, err := json.Marshal(readOutputArgs{Handle: handle, Offset: offset, Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		result, err := reg.Execute(ctx, "read_output", raw)
		if err != nil || result.Err != nil {
			t.Fatalf("page: %v / %v", err, result.Err)
		}
		if !utf8.ValidString(result.Text) {
			t.Fatal("invalid UTF-8 page")
		}
		_, body, ok := strings.Cut(result.Text, "\n")
		if !ok {
			t.Fatalf("missing page body: %q", result.Text)
		}
		match := nextRE.FindStringSubmatch(body)
		if match == nil {
			if !strings.HasSuffix(body, "\n[end of stored output]") {
				t.Fatalf("missing end marker: %q", body)
			}
			out.WriteString(strings.TrimSuffix(body, "\n[end of stored output]"))
			return out.String()
		}
		next, err := strconv.Atoi(match[1])
		if err != nil || next <= offset {
			t.Fatalf("page did not advance: offset=%d next=%d; %q", offset, next, result.Text)
		}
		out.WriteString(strings.TrimSuffix(body, "\n"+match[0]))
		offset = next
	}
	t.Fatal("pagination did not finish")
	return ""
}

func TestOutputPagingHintFollowsActualPreviewHead(t *testing.T) {
	fixtures := []struct{ name, text string }{
		{"ascii", strings.Repeat("x", 18000)},
		{"line", strings.Repeat("h", 2499) + "\nIMPORTANT_DIAGNOSTIC=" + strings.Repeat("d", 900) + "\n" + strings.Repeat("x", 16000)},
		{"crlf", strings.Repeat("h", 2498) + "\r\nIMPORTANT_DIAGNOSTIC=" + strings.Repeat("d", 900) + "\r\n" + strings.Repeat("x", 16000)},
		{"two_byte", strings.Repeat("h", 3071) + "ż" + strings.Repeat("x", 16000)},
		{"three_byte", strings.Repeat("h", 3071) + "界" + strings.Repeat("x", 16000)},
		{"four_byte", strings.Repeat("h", 3070) + "🙂" + strings.Repeat("x", 16000)},
	}
	for _, fixture := range fixtures {
		for _, cold := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cold=%v", fixture.name, cold), func(t *testing.T) {
				backend := &memoryOutputPersistence{}
				ctx := context.Background()
				if cold {
					ctx = WithOutputPersistence(ctx, backend)
				}
				s := NewOutputStore()
				preview := s.CompactContext(ctx, "read_lines", fixture.text)
				handle := onlyOutputHandle(t, s)
				_, body, ok := strings.Cut(preview, "\n")
				if !ok {
					t.Fatal("missing preview body")
				}
				head, _, ok := strings.Cut(body, "\n[... omitted_bytes=")
				if !ok {
					t.Fatal("missing omission marker")
				}
				offset, limit := parseHint(t, preview)
				if offset != len(head) {
					t.Errorf("hint starts at %d but visible head ends at %d (gap=%d)", offset, len(head), offset-len(head))
				}
				if cold {
					s = NewOutputStore()
				}
				remainder := walkBoundaryPages(t, ctx, s, handle, offset, limit)
				if got := head + remainder; got != fixture.text {
					t.Errorf("preview plus paged remainder lost bytes: got=%d want=%d", len(got), len(fixture.text))
				}
				if cold && (backend.saves != 1 || backend.reads != 1) {
					t.Fatalf("unexpected I/O: saves=%d reads=%d", backend.saves, backend.reads)
				}
			})
		}
	}
}

func TestOutputPagingTinyUTF8LimitAlwaysAdvances(t *testing.T) {
	for _, text := range []string{"żółw", "界中文", "🙂🚀✅", "Aż界🙂Z"} {
		for limit := 1; limit <= 4; limit++ {
			t.Run(fmt.Sprintf("%s/limit=%d", text, limit), func(t *testing.T) {
				s := NewOutputStore()
				handle, ok := s.put(text)
				if !ok {
					t.Fatal("retain")
				}
				if got := walkBoundaryPages(t, context.Background(), s, handle, 0, limit); got != text {
					t.Fatalf("lost text: %q want %q", got, text)
				}
			})
		}
	}
}

func TestOutputPagingUTF8OffsetsAndCaps(t *testing.T) {
	s := NewOutputStore()
	text := "Aż界🙂Z" + strings.Repeat("界", outputReadMax)
	handle, ok := s.put(text)
	if !ok {
		t.Fatal("retain")
	}
	for offset := 0; offset <= 12; offset++ {
		for _, limit := range []int{-1, 0, 1, 2, 3, 4, 7, outputReadDefault, outputReadMax, outputReadMax + 100} {
			chunk, start, total, err := s.read(handle, offset, limit)
			if err != nil {
				t.Fatal(err)
			}
			if start < offset || total != len(text) || len(chunk) == 0 || !utf8.ValidString(chunk) || chunk != text[start:start+len(chunk)] {
				t.Fatalf("offset=%d limit=%d: invalid page start=%d len=%d total=%d", offset, limit, start, len(chunk), total)
			}
			budget := limit
			if budget <= 0 {
				budget = outputReadDefault
			}
			budget = min(budget, outputReadMax)
			if len(chunk) > max(budget, utf8.UTFMax) || len(chunk) > outputReadMax {
				t.Fatalf("page exceeds cap: %d", len(chunk))
			}
		}
	}
	for _, text := range []string{"", "ż", "🙂"} {
		handle, _ := s.put(text)
		chunk, start, total, err := s.read(handle, len(text), 1)
		if err != nil || chunk != "" || start != len(text) || total != len(text) {
			t.Fatalf("EOF: %q %d %d %v", chunk, start, total, err)
		}
		for _, offset := range []int{-1, len(text) + 1} {
			if _, _, _, err := s.read(handle, offset, 1); err == nil {
				t.Fatalf("accepted invalid offset=%d", offset)
			}
		}
	}
}
