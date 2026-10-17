package viewport

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/muesli/termenv"
)

func readTestStyle(text string) cellbuf.Style {
	parser := ansi.GetParser()
	defer ansi.PutParser(parser)
	var style cellbuf.Style
	var state byte
	for len(text) > 0 {
		sequence, _, n, next := ansi.DecodeSequence(text, state, parser)
		if ansi.HasCsiPrefix(sequence) && parser.Command() == 'm' {
			cellbuf.ReadStyle(parser.Params(), &style)
		}
		text, state = text[n:], next
	}
	return style
}

func assertTextStyle(t *testing.T, line, text, sgr string) {
	t.Helper()
	prefix, _, found := strings.Cut(line, text)
	if !found {
		t.Fatalf("missing %q in %q", text, line)
	}
	got, want := readTestStyle(prefix), readTestStyle(sgr)
	if got.Sequence() != want.Sequence() {
		t.Fatalf("style before %q: got %q, want %q; line=%q", text, got.Sequence(), want.Sequence(), line)
	}
}

func TestViewportRetainsInheritedStyles(t *testing.T) {
	for _, tc := range []struct{ name, content, text, sgr string }{
		{"ANSI", "\x1b[31mhead\nred-body\nred-tail\x1b[0m\nplain", "red-body", "\x1b[31m"},
		{"selective resets", "\x1b[1;31;44mhead\n\x1b[22;49mred-body\n\x1b[39mplain", "red-body", "\x1b[31m"},
		{"truecolor and indexed background", "\x1b[38;2;1;2;3;48;5;17mhead\nbody\nend\x1b[0m", "body", "\x1b[38;2;1;2;3;48;5;17m"},
		{"colon SGR", "\x1b[38:2::1:2:3mhead\nbody\nend\x1b[m", "body", "\x1b[38;2;1;2;3m"},
		{"default reset", "\x1b[31mhead\n\x1b[mplain\nend", "plain", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(40, 1)
			m.SetContent(tc.content)
			raw := append([]string(nil), m.lines...)
			m.SetYOffset(1)
			line := m.visibleLines()[0]
			assertTextStyle(t, line, tc.text, tc.sgr)
			if ansi.Strip(line) != ansi.Strip(raw[1]) {
				t.Fatal("restoring styles changed the visible text")
			}
			after := readTestStyle(line)
			if !after.Empty() {
				t.Fatal("inherited style leaked past the rendered row")
			}
			if !reflect.DeepEqual(m.lines, raw) {
				t.Fatal("rendering changed raw viewport storage")
			}
		})
	}
}

func TestViewportRestoresStylesBeforeHorizontalCutAndScroll(t *testing.T) {
	m := New(7, 1)
	m.SetContent("\x1b[31mhead\nprefix colored-value\nlast\x1b[0m")
	m.SetYOffset(1)
	m.SetXOffset(7)
	line := m.visibleLines()[0]
	if ansi.Strip(line) != "colored" {
		t.Fatalf("horizontal cut changed text: %q", line)
	}
	assertTextStyle(t, line, "colored", "\x1b[31m")
	m.SetXOffset(0)
	m.GotoTop()
	assertTextStyle(t, m.ScrollDown(1)[0], "colored", "\x1b[31m")
	assertTextStyle(t, m.ScrollUp(1)[0], "head", "\x1b[31m")
}

func TestViewportStyleCacheKeepsCopiesIndependent(t *testing.T) {
	original := New(40, 1)
	original.SetContent("\x1b[31mhead\nbody\nend\x1b[0m")
	copied := original
	old := append([]lineStyle(nil), copied.lineStyles...)
	original.SetContent("\x1b[32mhead\nbody\nend\x1b[0m")
	if !reflect.DeepEqual(copied.lineStyles, old) {
		t.Fatal("updating original mutated copied checkpoints")
	}
	original.SetYOffset(1)
	copied.SetYOffset(1)
	assertTextStyle(t, original.visibleLines()[0], "body", "\x1b[32m")
	assertTextStyle(t, copied.visibleLines()[0], "body", "\x1b[31m")
	copied.SetContent("\x1b[mhead\nbody\nend\x1b[0m")
	assertTextStyle(t, copied.visibleLines()[0], "body", "")
	assertTextStyle(t, original.visibleLines()[0], "body", "\x1b[32m")
}

func TestViewportStyleCacheReusesUnchangedPrefix(t *testing.T) {
	m := New(40, 1)
	content := "\x1b[31m" + strings.Repeat("x", 65535) + "\nbody\nend\x1b[0m"
	m.SetContent(content)
	if m.lineWidths[0] != 0xffff || len(m.lineStyles) != 2 {
		t.Fatal("fixture must include an uncached width and sparse style boundaries")
	}
	first := &m.lineStyles[0]
	for _, text := range []string{content, content + "\nplain tail", content + "\nchanged tail", content} {
		m.SetContent(text)
		if &m.lineStyles[0] != first {
			t.Fatal("unchanged style checkpoints were recopied")
		}
		m.SetYOffset(1)
		assertTextStyle(t, m.visibleLines()[0], "body", "\x1b[31m")
	}
}

func TestViewportReplayPreservesPaletteVersusRGB(t *testing.T) {
	indexed := readTestStyle("\x1b[31m")
	r, g, b, _ := indexed.Fg.RGBA()
	rgb := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r>>8, g>>8, b>>8)
	m := New(40, 1)
	m.SetContent("\x1b[31mindexed\n" + rgb + "explicit\ninherited\x1b[0m")
	m.SetYOffset(2)
	assertTextStyle(t, m.visibleLines()[0], "inherited", rgb)
}

func TestViewportReplayPreservesBaseStyle(t *testing.T) {
	renderer := lipgloss.NewRenderer(io.Discard)
	renderer.SetColorProfile(termenv.TrueColor)
	m := New(40, 1)
	m.Style = renderer.NewStyle().Background(lipgloss.Color("#112233")).Bold(true)
	m.SetContent("\x1b[31mhead\nbody\x1b[0m")
	m.SetYOffset(1)
	assertTextStyle(t, m.View(), "body", "\x1b[1;31;48;2;17;34;51m")
}
