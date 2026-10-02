package tui

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func assertFreshHint(t *testing.T, m Model) string {
	t.Helper()
	got := m.renderHintLine()
	fresh := m
	fresh.renderCache = nil
	if want := fresh.renderHintLine(); got != want {
		t.Fatalf("cached hint differs from fresh hint: got %q want %q", got, want)
	}
	assertFreshRender(t, m)
	return got
}

func TestHintCacheWidthLanguageAndNoColorStayFresh(t *testing.T) {
	m := renderCacheTestModel()
	assertFreshHint(t, m)
	for _, width := range []int{180, 120, 80, 42, 18, 0} {
		for _, language := range []string{"en", "pl", "de", "uk"} {
			m.width, m.language = width, language
			assertFreshHint(t, m)
		}
	}
	renderer := lipgloss.NewRenderer(io.Discard)
	renderer.SetColorProfile(termenv.TrueColor)
	renderer.SetHasDarkBackground(true)
	m.width, m.language = 120, "en"
	m.palette = NewPalette(renderer)
	first := assertFreshHint(t, m)
	renderer.SetHasDarkBackground(false)
	assertFreshHint(t, m)
	renderer.SetColorProfile(termenv.Ascii)
	assertFreshHint(t, m)
	m.palette = NoColorPalette()
	if got := assertFreshHint(t, m); got == first {
		t.Fatal("NoColor replacement retained colored hints")
	}
	m.palette.InputHint = m.palette.InputHint.Padding(0, 2).Width(50)
	assertFreshHint(t, m)
}

func TestHintCacheStatusScrollAndMenuRemainLive(t *testing.T) {
	m := renderCacheTestModel()
	defaultHint := assertFreshHint(t, m)
	m.viewport.GotoTop()
	scrollHint := assertFreshHint(t, m)
	if scrollHint == defaultHint {
		t.Fatal("manual scroll did not show the scroll-back hint")
	}
	m.setStatus("fresh status on top of scroll-back", true)
	if got := assertFreshHint(t, m); !strings.Contains(got, m.statusOverride) {
		t.Fatal("status did not override scroll-back")
	}
	m.setStatus("", false)
	if got := assertFreshHint(t, m); got != scrollHint {
		t.Fatal("cleared status did not restore scroll-back")
	}
	m.viewport.GotoBottom()
	if got := assertFreshHint(t, m); got != defaultHint {
		t.Fatal("returning to bottom did not restore default hints")
	}
	next, _ := m.openActionsMenu()
	menuModel := next.(Model)
	before := assertFreshRender(t, menuModel)
	menuModel.menu.filter = "model"
	after := assertFreshRender(t, menuModel)
	if before == after {
		t.Fatal("active menu was stale after a direct filter change")
	}
	next, _ = menuModel.closeMenu()
	assertFreshHint(t, next.(Model))
}

func TestHintCacheCopiedModelsAndStatefulStylesStayFresh(t *testing.T) {
	original := renderCacheTestModel()
	originalHint := assertFreshHint(t, original)
	changed := original
	changed.width, changed.language = 48, "pl"
	changed.palette.InputHint = changed.palette.InputHint.Padding(0, 1)
	for i := 0; i < 3; i++ {
		if got := assertFreshHint(t, changed); got == originalHint {
			t.Fatal("changed copy retained original hints")
		}
		if got := assertFreshHint(t, original); got != originalHint {
			t.Fatal("rendering a copy altered original hints")
		}
	}
	suffix := " first suffix"
	original.palette.InputHint = original.palette.InputHint.Transform(func(s string) string { return s + suffix })
	first := assertFreshHint(t, original)
	suffix = " second suffix"
	if got := assertFreshHint(t, original); got == first || !strings.HasSuffix(got, suffix) {
		t.Fatal("stateful InputHint transform was hidden by cache")
	}
}

func BenchmarkHintLineCache(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "fresh"
		if enabled {
			name = "cached"
		}
		b.Run(name, func(b *testing.B) {
			m := renderCacheTestModel()
			if !enabled {
				m.renderCache = nil
			}
			_ = m.renderHintLine()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = m.renderHintLine()
			}
		})
	}
}
