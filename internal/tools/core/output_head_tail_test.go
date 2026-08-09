package core

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHeadTail_Fits(t *testing.T) {
	if got := HeadTail("hello", 10, 10); got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestHeadTail_Caps(t *testing.T) {
	s := "AAAA" + strings.Repeat("x", 100) + "ZZZZ"
	got := HeadTail(s, 4, 4)
	if !strings.HasPrefix(got, "AAAA\n[... omitted_bytes=100, omitted_lines=0 ...]\n") {
		t.Fatalf("head/marker wrong: %q", got)
	}
	if !strings.HasSuffix(got, "ZZZZ") {
		t.Fatalf("tail wrong: %q", got)
	}
}

func TestHeadTail_UTF8Boundaries(t *testing.T) {
	// "ż" is 2 bytes; head cut at 5 lands mid-rune and must move back.
	s := strings.Repeat("ż", 20) // 40 bytes
	got := HeadTail(s, 5, 5)
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8: %q", got)
	}
}

// The byte cuts are nudged to line boundaries when a newline is close
// (lineCutSlack), so neither head nor tail starts mid-line.
func TestHeadTail_LineBoundaryCuts(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, fmt.Sprintf("line %03d payload payload payload", i))
	}
	s := strings.Join(lines, "\n") + "\n"
	got := HeadTail(s, 80, 80)

	// The head must end at a newline (line boundary) and the tail must
	// start right after one.
	if !strings.HasSuffix(got[:strings.Index(got, "[... omitted")], "\n") {
		t.Fatalf("head does not end at a line boundary:\n%q", got)
	}
	markerEnd := strings.Index(got, "...]")
	if !strings.HasPrefix(got[markerEnd+4:], "\n") {
		t.Fatalf("tail does not start at a line boundary:\n%q", got)
	}
	// Every line is "line NNN payload..." = 27 bytes + '\n' = 28; the
	// kept head of ~80 bytes holds 2 full lines (56 bytes) plus slack
	// for the third, and the marker reports the real byte/loss counts.
	head := got[:strings.Index(got, "[... omitted")]
	for _, l := range strings.Split(head, "\n") {
		if l != "" && !strings.HasPrefix(l, "line ") {
			t.Fatalf("head contains a partial line: %q", l)
		}
	}
	tail := got[markerEnd+4:]
	for _, l := range strings.Split(tail, "\n") {
		if l != "" && !strings.HasPrefix(l, "line ") {
			t.Fatalf("tail contains a partial line: %q", l)
		}
	}
}

// A pathological single-line output still caps at the byte budget even
// though no line boundary exists nearby.
func TestHeadTail_NoNewlineNearCut(t *testing.T) {
	s := "Z" + strings.Repeat("y", 1000) + "Z"
	got := HeadTail(s, 16, 16)
	if len(got) > 16+16+64 {
		t.Fatalf("no-newline output not capped: %d bytes", len(got))
	}
	if !strings.HasPrefix(got, "Z") || !strings.HasSuffix(got, "Z") {
		t.Fatalf("head/tail lost: %q", got)
	}
}

// The omission marker reports how many complete lines were lost, not
// just bytes, so a line-oriented tool result tells the model the scale
// of the cut.
func TestHeadTail_CountsOmittedLines(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, fmt.Sprintf("line %03d", i))
	}
	s := strings.Join(lines, "\n") + "\n" // 100 lines, 9 bytes each
	got := HeadTail(s, 20, 20)
	if !regexp.MustCompile(`omitted_lines=\d+`).MatchString(got) {
		t.Fatalf("omitted_lines missing: %q", got)
	}
	// 100 lines * 10 bytes = 1000 bytes, head+tail ~40 bytes + marker:
	// roughly 96 lines are gone.
	m := regexp.MustCompile(`omitted_lines=(\d+)`).FindStringSubmatch(got)
	n, _ := strconv.Atoi(m[1])
	if n < 90 || n > 98 {
		t.Fatalf("omitted_lines=%d, want ~96", n)
	}
}

func TestHeadTailBuffer_NoTruncation(t *testing.T) {
	b := NewHeadTailBuffer(8, 8)
	b.Write([]byte("abc"))
	b.Write([]byte("def"))
	if b.Truncated() {
		t.Fatal("unexpected truncation")
	}
	if got := b.String(); got != "abcdef" {
		t.Fatalf("got %q", got)
	}
}

func TestHeadTailBuffer_DropsMiddle(t *testing.T) {
	b := NewHeadTailBuffer(4, 4)
	b.Write([]byte("HEAD"))
	for i := 0; i < 100; i++ {
		b.Write([]byte("xx"))
	}
	b.Write([]byte("TAIL"))
	if !b.Truncated() {
		t.Fatal("expected truncation")
	}
	got := b.String()
	if !strings.HasPrefix(got, "HEAD") {
		t.Fatalf("head lost: %q", got)
	}
	if !strings.HasSuffix(got, "TAIL") {
		t.Fatalf("tail lost: %q", got)
	}
	if !strings.Contains(got, "omitted_bytes=200") {
		t.Fatalf("omitted count wrong: %q", got)
	}
}

func TestHeadTailBuffer_HugeSingleWrite(t *testing.T) {
	b := NewHeadTailBuffer(4, 4)
	b.Write([]byte("HD" + strings.Repeat("x", 1000) + "TL"))
	got := b.String()
	if !strings.HasPrefix(got, "HDxx") || !strings.HasSuffix(got, "xxTL") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "omitted_bytes=996") {
		t.Fatalf("omitted count wrong: %q", got)
	}
}

func TestHeadTailBuffer_UTF8SafeCuts(t *testing.T) {
	b := NewHeadTailBuffer(5, 5)
	b.Write([]byte(strings.Repeat("ż", 50))) // 100 bytes, cuts land mid-rune
	got := b.String()
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8: %q", got)
	}
	if !strings.Contains(got, "omitted_bytes=") {
		t.Fatalf("marker missing: %q", got)
	}
}

// The buffer counts lines dropped DURING streaming (its omitted_lines
// cannot rely on the whole output, which is exactly why the count is
// tracked incrementally), and its cuts land on line boundaries.
func TestHeadTailBuffer_CountsOmittedLines(t *testing.T) {
	b := NewHeadTailBuffer(9, 9)
	for i := 0; i < 100; i++ {
		b.Write([]byte(fmt.Sprintf("line %03d\n", i))) // 10 bytes per line
	}
	got := b.String()
	m := regexp.MustCompile(`omitted_lines=(\d+)`).FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("omitted_lines missing: %q", got)
	}
	n, _ := strconv.Atoi(m[1])
	// 1000 bytes total, 18 kept: ~98 complete lines are gone.
	if n < 95 || n > 99 {
		t.Fatalf("omitted_lines=%d, want ~98", n)
	}
	// The kept head and tail consist of whole "line NNN" lines.
	marker := strings.Index(got, "[... omitted")
	if !strings.HasSuffix(got[:marker], "\n") {
		t.Fatalf("head does not end at a line boundary: %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("tail does not end on the final newline: %q", got)
	}
	// Line count and byte count must be consistent with the marker.
	bLine := regexp.MustCompile(`omitted_bytes=(\d+)`).FindStringSubmatch(got)
	bytesN, _ := strconv.Atoi(bLine[1])
	if bytesN < 880 || bytesN > 920 {
		t.Fatalf("omitted_bytes=%d, want ~900", bytesN)
	}
}
