package files

import (
	"strings"
	"testing"

	"supercli/internal/tools/fileops"
)

func TestRenderLines_LongLineTruncated(t *testing.T) {
	long := strings.Repeat("x", maxReadLineChars+500)
	out := renderLines([]fileops.LineRange{{Number: 1, Content: long}})
	// Count the run of x's before the marker (the marker text
	// "ctx_execute" itself contains x's, so count the body run).
	body := out
	if idx := strings.Index(out, " …["); idx >= 0 {
		body = out[:idx]
	}
	if strings.Count(body, "x") > maxReadLineChars {
		t.Fatalf("long line was not truncated: got %d x's", strings.Count(body, "x"))
	}
	if !strings.Contains(out, "use ctx_execute for the full line") {
		t.Fatalf("missing per-line truncation marker: %q", out)
	}
}

func TestRenderLines_ByteBudget(t *testing.T) {
	// Many moderately long lines that together blow the byte
	// budget; rendering must stop and emit a guidance marker.
	var lines []fileops.LineRange
	body := strings.Repeat("y", 1000)
	for i := 1; i <= 500; i++ {
		lines = append(lines, fileops.LineRange{Number: i, Content: body})
	}
	out := renderLines(lines)
	if len(out) > maxReadOutputBytes+512 {
		t.Fatalf("output exceeded byte budget: %d bytes", len(out))
	}
	if !strings.Contains(out, "output truncated at") || !strings.Contains(out, "request line") {
		t.Fatalf("missing byte-budget truncation marker: tail=%q", out[len(out)-200:])
	}
}

func TestRenderLines_ShortStaysWhole(t *testing.T) {
	out := renderLines([]fileops.LineRange{
		{Number: 1, Content: "hello"},
		{Number: 2, Content: "world"},
	})
	if !strings.Contains(out, "   1 | hello") || !strings.Contains(out, "   2 | world") {
		t.Fatalf("short lines mangled: %q", out)
	}
	if strings.Contains(out, "truncated") {
		t.Fatalf("unexpected truncation marker: %q", out)
	}
}
