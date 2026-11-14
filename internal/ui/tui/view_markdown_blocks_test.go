package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestMarkdownHeadingsDropMarkers(t *testing.T) {
	p := NoColorPalette()
	out := renderAssistantMarkdown("# Top\n## Plan\n### Step\n#hashtag stays", p, false)
	lines := strings.Split(ansi.Strip(out), "\n")
	want := []string{"Top", "═══", "Plan", "────", "Step", "#hashtag stays"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("headings:\n%q\nwant\n%q", lines, want)
	}
}

func TestMarkdownTableAlignsColumnsByDisplayWidth(t *testing.T) {
	p := NoColorPalette()
	text := "| Plik | Linie | Uwagi |\n|------|------:|:-----:|\n| src/server.go | 16 | błąd |\n| 中文.md | 3 | ok |"
	got := strings.Split(ansi.Strip(renderAssistantMarkdown(text, p, false)), "\n")
	want := []string{
		"Plik          │ Linie │ Uwagi",
		"──────────────┼───────┼──────",
		"src/server.go │    16 │ błąd",
		"中文.md       │     3 │  ok",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("table:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// A header still streaming without its separator stays an ordinary line.
	if out := ansi.Strip(renderAssistantMarkdown("| a | b |", p, false)); out != "| a | b |" {
		t.Fatalf("partial table: %q", out)
	}
}

func TestMarkdownQuoteAndFenceFrame(t *testing.T) {
	p := NoColorPalette()
	out := ansi.Strip(renderAssistantMarkdown("> Uwaga: **ważne**\n```go\nx := 1\n```", p, false))
	want := "│ Uwaga: ważne\n╭─ go\n│ x := 1\n╰─"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
	// An unfinished streaming fence has no closing frame yet.
	if out := ansi.Strip(renderAssistantMarkdown("```\npartial", p, false)); out != "╭─\n│ partial" {
		t.Fatalf("streaming fence: %q", out)
	}
}

func TestDiffMarkerColoursLines(t *testing.T) {
	m := NewMarker(codeTestPalette(termenv.TrueColor, true))
	text := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n ctx"
	out := m.Diff(text)
	if ansi.Strip(out) != text {
		t.Fatalf("diff text changed: %q", ansi.Strip(out))
	}
	lines := strings.Split(out, "\n")
	if lines[4] == lines[5] || !strings.Contains(lines[5], "\x1b[") || !strings.Contains(lines[3], "\x1b[") {
		t.Fatalf("added/removed/hunk lines are not coloured distinctly: %q", lines)
	}
	if strings.Contains(NewMarker(NoColorPalette()).Diff(text), "\x1b") {
		t.Fatal("no-colour diff must stay plain")
	}
}
