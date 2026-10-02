package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"supercli/internal/agent"
)

func activeSectionFixture() (chat, Palette) {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.TrueColor)
	r.SetHasDarkBackground(true)
	p := NewPalette(r)
	c := newChat(72, "en")
	c.legacySymbols = false
	c.current = "<thinking>Zażółć 中国 😀 \x1b[2mthinking\x1b[0m</thinking>\n## Result\nAnswer with **bold**, \x60code\x60, ANSI \x1b[31mred\x1b[0m and a long wrapped line.\n### Subheading\n"
	return c, p
}

func assertFreshActiveSection(t *testing.T, c *chat, p Palette, spinner string) string {
	t.Helper()
	got := c.renderWithSpinner(p, spinner)
	fresh := *c
	fresh.activeCache = nil
	fresh.completedDirty = true
	want := fresh.renderWithSpinner(p, spinner)
	if got != want {
		t.Fatalf("cached active section differs from complete rendering:\ncached=%q\nfresh=%q", got, want)
	}
	return got
}

func TestActiveSectionCachePreservesSpinnerAndRenderingInputs(t *testing.T) {
	tests := []struct {
		name   string
		change func(*chat, *Palette)
	}{
		{"delta", func(c *chat, _ *Palette) { c.current += "\nMore 中文 and Polish żółć." }},
		{"replacement", func(c *chat, _ *Palette) { c.current = "A replaced source snapshot." }},
		{"width", func(c *chat, _ *Palette) { c.width = 24 }},
		{"language", func(c *chat, _ *Palette) { c.language = "pl" }},
		{"thinking", func(c *chat, _ *Palette) { c.toggleThinking() }},
		{"legacy_symbols", func(c *chat, _ *Palette) { c.legacySymbols = true }},
		{"theme", func(_ *chat, p *Palette) {
			r := lipgloss.NewRenderer(io.Discard)
			r.SetColorProfile(termenv.ANSI256)
			*p = NewPalette(r)
		}},
		{"color_profile", func(_ *chat, p *Palette) { p.renderer.SetColorProfile(termenv.Ascii) }},
		{"background", func(_ *chat, p *Palette) { p.renderer.SetHasDarkBackground(false) }},
		{"assistant", func(_ *chat, p *Palette) { p.Assistant = p.Assistant.Italic(true) }},
		{"assistant_label", func(_ *chat, p *Palette) { p.AssistantLabel = p.AssistantLabel.Underline(true) }},
		{"gutter", func(_ *chat, p *Palette) { p.AssistGutter = p.AssistGutter.Foreground(lipgloss.Color("45")) }},
		{"h2", func(_ *chat, p *Palette) { p.MdH2 = p.MdH2.Foreground(lipgloss.Color("51")) }},
		{"h3", func(_ *chat, p *Palette) {
			p.MdH3 = p.MdH3.Foreground(lipgloss.Color("52"))
		}},
		{"code", func(_ *chat, p *Palette) { p.MdCode = p.MdCode.Underline(true) }},
		{"thinking_style", func(_ *chat, p *Palette) { p.MdThinking = p.MdThinking.Italic(true) }},
		{"thinking_label", func(_ *chat, p *Palette) { p.MdThinkingHeader = p.MdThinkingHeader.Underline(true) }},
		{"bold", func(_ *chat, p *Palette) { p.Bold = p.Bold.Foreground(lipgloss.Color("201")) }},
		{"history", func(c *chat, _ *Palette) { c.addSystem("Tool completed while waiting for more text.") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, p := activeSectionFixture()
			first := assertFreshActiveSection(t, &c, p, "spin-A")
			if c.activeCache == nil {
				t.Fatal("active section was not cached")
			}
			source := c.current
			second := assertFreshActiveSection(t, &c, p, "spin-B")
			if first == second || !strings.Contains(second, "spin-B") || c.current != source {
				t.Fatal("spinner was cached or source text was changed")
			}
			tt.change(&c, &p)
			assertFreshActiveSection(t, &c, p, "spin-C")
		})
	}
}

func TestActiveSectionCacheDoesNotHideStatefulTransforms(t *testing.T) {
	c, p := activeSectionFixture()
	c.current = "One answer line."
	calls := 0
	p.Assistant = p.Assistant.Transform(func(text string) string {
		calls++
		return fmt.Sprintf("%d:%s", calls, text)
	})
	first := c.renderWithSpinner(p, "spin-A")
	before := calls
	second := c.renderWithSpinner(p, "spin-B")
	if calls <= before || first == strings.ReplaceAll(second, "spin-B", "spin-A") {
		t.Fatal("cache hid the custom style transform")
	}
}

func TestActiveSectionCacheCopiedChatsRemainIndependent(t *testing.T) {
	original, p := activeSectionFixture()
	before := assertFreshActiveSection(t, &original, p, "spin-A")
	copyChat := original
	copyChat.current = "A separate copied model response."
	copyChat.width = 25
	assertFreshActiveSection(t, &copyChat, p, "spin-B")
	if got := assertFreshActiveSection(t, &original, p, "spin-A"); got != before {
		t.Fatal("advancing a copied chat changed the original view")
	}
	copyChat.flushCurrent()
	if copyChat.activeCache != nil || copyChat.current != "" {
		t.Fatal("flushing retained active source or formatted section")
	}
	if got := assertFreshActiveSection(t, &original, p, "spin-A"); got != before {
		t.Fatal("clearing a copied cache changed the original")
	}
}

func TestActiveSectionCacheConcurrentCopiedChats(t *testing.T) {
	original, p := activeSectionFixture()
	_ = original.renderWithSpinner(p, "spin-A")
	other := original
	other.current = "Independent copied reply with 中文 **formatting**."
	left, right := original, other
	left.activeCache, right.activeCache = nil, nil
	wantOriginal := left.renderWithSpinner(p, "spin-A")
	wantOther := right.renderWithSpinner(p, "spin-B")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if got := original.renderWithSpinner(p, "spin-A"); got != wantOriginal {
				t.Errorf("original copied chat changed: %q", got)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if got := other.renderWithSpinner(p, "spin-B"); got != wantOther {
				t.Errorf("other copied chat changed: %q", got)
				return
			}
		}
	}()
	wg.Wait()
}

func TestActiveSectionCacheImmediateTextAndReasoningBoundary(t *testing.T) {
	m := streamTestModel(t)
	next, _ := m.Update(runEventMsg{ev: agent.ReasoningEvent{Text: "Zażółć 中文 😀 thought."}})
	m = next.(Model)
	if !strings.Contains(m.viewport.View(), "Zażółć") || m.chat.activeCache == nil {
		t.Fatal("first reasoning fragment was not prepared immediately")
	}
	next, _ = m.Update(runEventMsg{ev: agent.MessageEvent{Text: "Answer immediately after thinking."}})
	m = next.(Model)
	if !strings.Contains(m.viewport.View(), "Answer immediately after thinking.") {
		t.Fatal("reasoning-to-answer boundary waited for another tick")
	}
	for _, delta := range []string{" More text.", " \x1b[31mred\x1b[0m.", " 中文."} {
		next, _ = m.Update(runEventMsg{ev: agent.MessageEvent{Text: delta}})
		m = next.(Model)
		next, _ = m.Update(streamFlushMsg{})
		m = next.(Model)
		if !strings.Contains(m.viewport.View(), strings.TrimSpace(delta)) && !strings.Contains(delta, "\x1b") {
			t.Fatalf("delta was not displayed: %q", delta)
		}
	}
	assertFreshActiveSection(t, &m.chat, m.palette, m.streamSpinner())
}

func TestActiveSectionCacheClearedAtTerminalBoundaries(t *testing.T) {
	for _, boundary := range []string{"done", "error", "tool", "run_end", "reset"} {
		t.Run(boundary, func(t *testing.T) {
			m := streamTestModel(t)
			next, _ := m.Update(runEventMsg{ev: agent.MessageEvent{Text: "Final answer 中文."}})
			m = next.(Model)
			if m.chat.activeCache == nil {
				t.Fatal("fixture was not cached")
			}
			switch boundary {
			case "done":
				next, _ = m.Update(runEventMsg{ev: agent.DoneEvent{Usage: agent.Usage{Input: 5, Output: 5}}})
				m = next.(Model)
			case "error":
				next, _ = m.Update(runEventMsg{ev: agent.ErrorEvent{Err: fmt.Errorf("fixture cancelled")}})
				m = next.(Model)
			case "tool":
				next, _ = m.Update(runEventMsg{ev: agent.ToolCallEvent{ID: "call1", Name: "read_lines", Args: "{}"}})
				m = next.(Model)
			case "run_end":
				next, _ = m.Update(runEndMsg{})
				m = next.(Model)
			case "reset":
				m.resetCurrent()
			}
			if m.chat.activeCache != nil {
				t.Fatalf("%s retained active cache", boundary)
			}
			if boundary != "reset" && !strings.Contains(m.viewport.View(), "Final answer") {
				t.Fatalf("%s lost the visible answer", boundary)
			}
		})
	}
}
