package tui

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"reflect"
	"strings"
	"testing"
)

// Independent full rebuild is the previous completed transcript renderer.
func rebuildCompletedHistoryForTest(c *chat, p Palette) string {
	var b strings.Builder
	for i, m := range c.msgs {
		if i > 0 && (m.role == roleUser || m.role == roleAssistant) {
			b.WriteByte('\n')
		}
		rendered, fits := c.renderMsgWrapHint(m, p, true)
		if !fits {
			rendered = ansi.Wrap(rendered, max(1, c.width), "")
		}
		b.WriteString(rendered)
		b.WriteByte('\n')
	}
	return b.String()
}
func assertCompletedHistoryParity(t *testing.T, c *chat, p Palette) {
	t.Helper()
	reference := *c
	want := rebuildCompletedHistoryForTest(&reference, p)
	got := c.renderCompleted(p)
	if got != want {
		t.Fatalf("completed bytes differ: got %q want %q", got, want)
	}
}
func TestCompletedHistoryAppendParity(t *testing.T) {
	languages := []string{"en", "bg", "cs", "da", "de", "el", "es", "et", "fi", "fr", "hr", "hu", "it", "lt", "lv", "nb", "nl", "pl", "pt-BR", "ro", "ru", "sk", "sl", "sr-Latn", "sv", "tr", "uk"}
	for _, lang := range languages {
		for _, width := range []int{1, 3, 5, 17, 80} {
			for _, profile := range []termenv.Profile{termenv.Ascii, termenv.ANSI256} {
				t.Run(fmt.Sprintf("%s/%d/%d", lang, width, profile), func(t *testing.T) {
					r := lipgloss.NewRenderer(&strings.Builder{})
					r.SetColorProfile(profile)
					r.SetHasDarkBackground(true)
					p := NewPalette(r)
					c := newChat(width, lang)
					c.legacySymbols = false
					operations := []func(){
						func() { c.addUser("Hello Żółć 中文 é 😀\nnext") },
						func() { c.addAssistant("<thinking>thinking\nline</thinking>\n**answer** \tvalue\n## Heading") },
						func() { c.addToolResult("ctx_execute", "{\"stdout\":\"generated\\nsecond\",\"stderr\":\"warn\"}", "") },
						func() { c.addSystem("transient") },
						func() { c.addToolResult("read_lines", "", "failure") },
						func() { c.addDocument("## Report\n| key | value |\ncode\n~~~") },
						func() { c.toggleMessage(0) },
						func() { c.toggleThinking() },
						func() { c.toolsExpanded = !c.toolsExpanded; c.completedDirty = true },
						func() { c.removeLastSystem("transient") },
						func() { c.width += 2; c.completedDirty = true },
						func() { c.language = "pl"; c.completedDirty = true },
						func() { c.legacySymbols = true; c.completedDirty = true },
						func() { c.current = "last streamed body"; c.flushCurrent() },
						func() { c.msgs = nil; c.completedDirty = true },
						func() { c.addAssistant("new transcript") },
					}
					for _, operation := range operations {
						operation()
						assertCompletedHistoryParity(t, &c, p)
					}
					r.SetHasDarkBackground(false)
					c.addSystem("changed terminal background")
					assertCompletedHistoryParity(t, &c, p)
					r.SetColorProfile(termenv.Ascii)
					c.addUser("changed terminal profile")
					assertCompletedHistoryParity(t, &c, p)
					p.User = p.User.Padding(0, 1)
					c.addSystem("custom style")
					assertCompletedHistoryParity(t, &c, p)
				})
			}
		}
	}
}
func TestCompletedHistorySharedMessages(t *testing.T) {
	p := NoColorPalette()
	c := newChat(40)
	c.legacySymbols = false
	c.msgs = make([]msg, 0, 16)
	c.addUser("shared\nbody")
	c.addAssistant("original")
	assertCompletedHistoryParity(t, &c, p)
	copied := c
	copied.toggleMessage(0)
	c.addSystem("append after alias fold")
	assertCompletedHistoryParity(t, &c, p)
	copied.msgs[1].text = "changed through copied backing"
	c.addAssistant("append after alias text")
	assertCompletedHistoryParity(t, &c, p)
	left := c
	right := c
	left.addSystem("left tail")
	assertCompletedHistoryParity(t, &left, p)
	right.addSystem("right overwrites shared tail")
	assertCompletedHistoryParity(t, &right, p)
	left.addAssistant("left appends again")
	assertCompletedHistoryParity(t, &left, p)
	c.msgs = c.msgs[:1]
	c.completedDirty = true
	assertCompletedHistoryParity(t, &c, p)
}
func TestCompletedHistoryCustomCallbackOrder(t *testing.T) {
	c := newChat(40)
	c.legacySymbols = false
	c.addUser("prompt")
	c.addAssistant("answer")
	p := NoColorPalette()
	var seen []string
	p.UserLabel = p.UserLabel.Transform(func(s string) string { seen = append(seen, "label"); return s })
	p.User = p.User.Transform(func(s string) string { seen = append(seen, "body"); return s })
	p.UserGutter = p.UserGutter.Transform(func(s string) string { seen = append(seen, "gutter"); return s })
	first := c.renderCompleted(p)
	firstCalls := append([]string(nil), seen...)
	seen = nil
	c.addSystem("tail")
	got := c.renderCompleted(p)
	if got != first+"tail\n" || !reflect.DeepEqual(seen, firstCalls) {
		t.Fatalf("callbacks skipped/reordered: %v want %v", seen, firstCalls)
	}
	// Preserve the full renderer's original slice even if a callback replaces it.
	p.UserLabel = p.UserLabel.Transform(func(s string) string { c.msgs = nil; return s })
	c.completedDirty = true
	if got := c.renderCompleted(p); !strings.Contains(got, "answer") {
		t.Fatal("callback changed range semantics")
	}
}

func TestCompletedHistorySnapshotLifetimeAndCopy(t *testing.T) {
	p := NoColorPalette()
	original := newChat(80)
	original.legacySymbols = false
	original.addUser("shared original")
	original.renderCompleted(p)
	snapshot := original.completedSnapshot
	if snapshot == nil {
		t.Fatal("stock history did not publish snapshot")
	}
	copied := original
	copied.addAssistant("copied append")
	copied.renderCompleted(p)
	if copied.completedSnapshot == snapshot || original.completedSnapshot != snapshot || len(snapshot.messages) != 1 {
		t.Fatal("copy mutated published snapshot")
	}
	copied.msgs = nil
	copied.completedDirty = true
	if copied.renderCompleted(p) != "" || copied.completedSnapshot != nil {
		t.Fatal("empty render retained old history")
	}
	copied = newChat(80)
	if copied.completedSnapshot != nil {
		t.Fatal("reset retained snapshot")
	}
	original.addSystem("custom palette must rebuild")
	p.User = p.User.Padding(0, 1)
	original.renderCompleted(p)
	if original.completedSnapshot != nil {
		t.Fatal("custom palette published reusable prefix")
	}
}
