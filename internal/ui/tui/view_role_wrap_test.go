package tui

import (
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"supercli/internal/system/uilang"
)

func wrapTestPalette(kind string) (Palette, *[]string) {
	p := NoColorPalette()
	log := []string{}
	switch kind {
	case "color":
		r := lipgloss.NewRenderer(io.Discard)
		r.SetColorProfile(termenv.TrueColor)
		r.SetHasDarkBackground(true)
		p = NewPalette(r)
	case "wide-label":
		p.UserLabel = p.UserLabel.Transform(func(s string) string { return s + strings.Repeat(" LONG", 8) })
		p.AssistantLabel = p.AssistantLabel.Transform(func(s string) string { return "中文長標籤\n" + s })
	case "wide-gutter":
		p.UserGutter = p.UserGutter.Transform(func(s string) string { return "中文" })
		p.AssistGutter = p.AssistGutter.Padding(0, 2).Border(lipgloss.RoundedBorder())
	case "control-label":
		p.UserLabel = p.UserLabel.Transform(func(s string) string { return "\x1b]8;;https://fixture.invalid\a" + s + "\x1b]8;;\a" })
		p.AssistantLabel = p.AssistantLabel.Transform(func(s string) string { return "tab\t" + s + "\r" })
	case "stateful":
		counter := 0
		mark := func(name string) func(string) string {
			return func(s string) string {
				counter++
				log = append(log, fmt.Sprintf("%s:%d:%s", name, counter, s))
				if name == "gutter" && counter%3 == 0 {
					return "wide gutter "
				}
				return s
			}
		}
		p.UserLabel = p.UserLabel.Transform(mark("label"))
		p.User = p.User.Transform(mark("userbody"))
		p.AssistantLabel = p.AssistantLabel.Transform(mark("assistantlabel"))
		p.Assistant = p.Assistant.Transform(mark("mdtext"))
		p.Bold = p.Bold.Transform(mark("bold"))
		p.UserGutter = p.UserGutter.Transform(mark("gutter"))
		p.AssistGutter = p.AssistGutter.Transform(mark("gutter"))
	case "body-border":
		p.User = p.User.Border(lipgloss.DoubleBorder()).Padding(1, 2)
		p.Assistant = p.Assistant.Margin(1, 3).Padding(0, 2)
	}
	return p, &log
}

func TestCompletedRoleWrapPreservesBytesAndStyleCalls(t *testing.T) {
	texts := []string{"", "ordinary text", "  leading and trailing whitespace  ", strings.Repeat("A **formatted** item with code and - breakpoints. ", 20), "one\ntwo\n\nthree\n", "# Heading\n\n- first\n- second\n\n" + strings.Repeat("longword", 20), "<thinking>Thought line.\nMore thought.</thinking>\n\nFinal **answer**.", "你好世界 日本語 Ελληνικά Русский Polski 😀 👍🏽 👨‍👩‍👧‍👦", "á ë \u0301 initial-combining \u00a0 nonbreaking space", "\t tab\r carriage\x00nul\x7fdel\x01control", "unicode\u0085nextline\u2028separator\u2003space", "\x1b[31mred text\x1b[0m plain", "\x1b[38:2:255:0:10mcolon sgr\x1b[m", "\x1b[2Jcursor\x1b[10Ccontrol", "\x1b]8;;https://fixture.invalid\alink\x1b]8;;\a", "\x1b[31 malformed \x1b[0", string([]byte{0xff, 0xfe, 'a', '\n'})}
	for _, kind := range []string{"plain", "color", "wide-label", "wide-gutter", "control-label", "stateful", "body-border"} {
		for _, width := range []int{0, 1, 2, 3, 4, 5, 7, 10, 17, 40, 100} {
			for ti, text := range texts {
				for _, who := range []role{roleUser, roleAssistant} {
					for _, fold := range []bool{false, true} {
						c := newChat(width, "en")
						c.thinkingCollapsed = fold
						c.legacySymbols = false
						m := msg{role: who, text: text}
						c.msgs = []msg{m}
						c.completedDirty = true
						expectedPalette, expectedLog := wrapTestPalette(kind)
						actualPalette, actualLog := wrapTestPalette(kind)
						want := ansi.Wrap(c.renderMsg(m, expectedPalette), max(1, width), "") + "\n"
						got := c.renderCompleted(actualPalette)
						if got != want {
							t.Fatalf("kind=%s width=%d text=%d role=%d fold=%v mismatch\nwant=%q\ngot=%q", kind, width, ti, who, fold, want, got)
						}
						if fmt.Sprint(*actualLog) != fmt.Sprint(*expectedLog) {
							t.Fatalf("style callback count/order changed kind=%s width=%d text=%d role=%d", kind, width, ti, who)
						}
					}
				}
			}
		}
	}
	for _, language := range uilang.Languages() {
		for _, width := range []int{4, 10, 80} {
			for _, who := range []role{roleUser, roleAssistant} {
				c := newChat(width, language.Code)
				m := msg{role: who, text: "ordinary text"}
				c.msgs = []msg{m}
				c.completedDirty = true
				p := NoColorPalette()
				if got, want := c.renderCompleted(p), ansi.Wrap(c.renderMsg(m, p), max(1, width), "")+"\n"; got != want {
					t.Fatalf("language %s width=%d role=%d mismatch", language.Code, width, who)
				}
			}
		}
	}
}
func TestCompletedRoleWrapUnicodeRegression(t *testing.T) {
	c := newChat(5, "en")
	c.legacySymbols = false
	c.addUser("á ë \u0301 initial-combining \u00a0 nonbreaking space")
	want := "You\n▌ á ë\n▌ ́\nini\n▌ tia\n▌ l-\n▌ com\n▌ bin\n▌ ing\n▌ \u00a0\n▌ non\n▌ bre\n▌ aki\n▌ ng\n▌ spa\n▌ ce\n"
	if got := c.renderCompleted(NoColorPalette()); got != want {
		t.Fatalf("combining mark + NBSP changed legacy wrapping\nwant=%q\ngot=%q", want, got)
	}
}
func TestCompletedRoleWrapRandomParity(t *testing.T) {
	r := rand.New(rand.NewSource(47))
	atoms := []string{"a", " ", "-", "\n", "中文", "😀", "👍🏽", "é", "\u00a0", "\t", "\r", "\x1b[31m", "\x1b[0m", "\x1b[2J", "**bold**", "_italic_", "\u0301", "👨‍👩‍👧‍👦", "\u2028"}
	for sample := 0; sample < 1200; sample++ {
		var text strings.Builder
		for n := r.Intn(80); n > 0; n-- {
			text.WriteString(atoms[r.Intn(len(atoms))])
		}
		width := 1 + r.Intn(80)
		for _, who := range []role{roleUser, roleAssistant} {
			c := newChat(width, "pl")
			c.legacySymbols = sample%2 == 0
			m := msg{role: who, text: text.String()}
			c.msgs = []msg{m}
			c.completedDirty = true
			p := NoColorPalette()
			if got, want := c.renderCompleted(p), ansi.Wrap(c.renderMsg(m, p), width, "")+"\n"; got != want {
				t.Fatalf("sample=%d width=%d role=%d text=%q mismatch\nwant=%q\ngot=%q", sample, width, who, text.String(), want, got)
			}
		}
	}
}
