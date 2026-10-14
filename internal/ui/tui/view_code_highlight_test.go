package tui

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func codeTestPalette(profile termenv.Profile, dark bool) Palette {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(profile)
	r.SetHasDarkBackground(dark)
	return NewPalette(r)
}

func TestFencedCodeKnownLanguageIsHighlighted(t *testing.T) {
	text := "Code:\n```go\nfunc main() {\n\treturn \"x\" // done\n}\n```\nafter"
	for _, dark := range []bool{true, false} {
		p := codeTestPalette(termenv.TrueColor, dark)
		out := renderAssistantMarkdown(text, p, false)
		if !strings.Contains(out, "\x1b[38;2;") {
			t.Fatalf("dark=%v: go block has no truecolor escapes: %q", dark, out)
		}
		// Different keyword/string/comment colours → several distinct escapes.
		if strings.Count(out, "\x1b[") < 6 {
			t.Fatalf("dark=%v: too few colour runs: %q", dark, out)
		}
		stripped := ansi.Strip(out)
		for _, want := range []string{"```go", "func main() {", "    return \"x\" // done", "```", "after"} {
			if !strings.Contains(stripped, want) {
				t.Fatalf("dark=%v: visible text lost %q: %q", dark, want, stripped)
			}
		}
	}
	dark := renderAssistantMarkdown(text, codeTestPalette(termenv.TrueColor, true), false)
	light := renderAssistantMarkdown(text, codeTestPalette(termenv.TrueColor, false), false)
	if dark == light {
		t.Fatal("light and dark backgrounds should use different code styles")
	}
	if out := renderAssistantMarkdown(text, codeTestPalette(termenv.ANSI256, true), false); !strings.Contains(out, "\x1b[38;5;") || strings.Contains(out, "38;2;") {
		t.Fatalf("ANSI256 profile must degrade colours: %q", out)
	}
}

func TestFencedCodeUnknownOrMissingLanguageIsPlain(t *testing.T) {
	p := codeTestPalette(termenv.TrueColor, true)
	for _, fence := range []string{"```nosuchlang-xyz", "```", "```text"} {
		lines := []string{fence, "func main() { return 1 }", "```"}
		if got := highlightCodeLines(fenceLanguage(fence), lines[1:2], p); got != nil {
			t.Fatalf("%q: expected plain fallback, got %q", fence, got)
		}
		got, next := renderFencedBlock(lines, 0, p)
		if next != 3 {
			t.Fatalf("%q: next=%d", fence, next)
		}
		if want := renderMarkdownBodyPlainLines(lines, p); got != want {
			t.Fatalf("%q: plain block changed\n got %q\nwant %q", fence, got, want)
		}
	}
}

func renderMarkdownBodyPlainLines(lines []string, p Palette) string {
	parts := make([]string, len(lines))
	for i, line := range lines {
		parts[i] = renderMarkdownLine(line, p)
	}
	return strings.Join(parts, "\n")
}

func TestFencedCodeNoColorHasNoEscapes(t *testing.T) {
	text := "```python\ndef f(x):\n    return x + 1  # comment\n```"
	for name, p := range map[string]Palette{"ascii": codeTestPalette(termenv.Ascii, true), "no_color": NoColorPalette()} {
		out := renderAssistantMarkdown(text, p, false)
		if strings.Contains(out, "\x1b") {
			t.Fatalf("%s: escapes in output: %q", name, out)
		}
		if !strings.Contains(out, "return x + 1  # comment") {
			t.Fatalf("%s: code text lost: %q", name, out)
		}
	}
}

func TestFencedCodeRespectsWidth(t *testing.T) {
	long := "const s = \"" + strings.Repeat("abcdefghij ", 20) + "\" // " + strings.Repeat("comment ", 15)
	text := "```js\n" + long + "\nlet x = 1;\n```"
	p := codeTestPalette(termenv.TrueColor, true)
	for _, width := range []int{12, 40, 80} {
		c := newChat(width, "en")
		c.addAssistant(text)
		out := c.renderMsg(c.msgs[len(c.msgs)-1], p)
		if !strings.Contains(out, "\x1b[38;2;") {
			t.Fatalf("width %d: not highlighted", width)
		}
		for _, line := range strings.Split(out, "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Fatalf("width %d: line width %d: %q", width, w, line)
			}
		}
	}
}

func TestFencedCodeUnfinishedStreamingBlock(t *testing.T) {
	p := codeTestPalette(termenv.TrueColor, true)
	text := "Here:\n```go\npackage main\nfunc main() {"
	out := renderAssistantMarkdown(text, p, false)
	if !strings.Contains(out, "\x1b[38;2;") {
		t.Fatalf("unfinished block not highlighted: %q", out)
	}
	if !strings.HasSuffix(ansi.Strip(out), "func main() {") {
		t.Fatalf("unfinished block text: %q", ansi.Strip(out))
	}
	// Text after an unclosed fence stays code; a later close resumes markdown.
	closed := renderAssistantMarkdown(text+"\n}\n```\n- item", p, false)
	if !strings.Contains(ansi.Strip(closed), "\u2022 item") {
		t.Fatalf("markdown after closing fence: %q", ansi.Strip(closed))
	}
}

func TestFencedCodeCacheReturnsSameResult(t *testing.T) {
	p := codeTestPalette(termenv.TrueColor, true)
	lines := []string{"x := 1", "y := x"}
	first := highlightCodeLines("go", lines, p)
	second := highlightCodeLines("go", lines, p)
	if first == nil || len(first) != 2 || &first[0] != &second[0] {
		t.Fatalf("expected cached slice reuse: %q %q", first, second)
	}
	p.renderer.SetHasDarkBackground(false)
	if third := highlightCodeLines("go", lines, p); &third[0] == &first[0] {
		t.Fatal("background change must miss the cache")
	}
}

func TestFenceLanguage(t *testing.T) {
	for in, want := range map[string]string{"```go": "go", "  ``` Python title=x": "python", "```{.rust}": "rust", "```": ""} {
		if got := fenceLanguage(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}

func BenchmarkFencedCodeStreamingFrame(b *testing.B) {
	p := codeTestPalette(termenv.TrueColor, true)
	text := "```go\n" + strings.Repeat("func f(a int) string { return fmt.Sprint(a) } // c\n", 80) + "```"
	for i := 0; i < b.N; i++ {
		renderAssistantMarkdown(text, p, false)
	}
}
