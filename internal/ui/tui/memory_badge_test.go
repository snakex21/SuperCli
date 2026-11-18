package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"supercli/internal/system/uilang"
)

// The "memory off" badge is permanent in the header (the TUI counterpart of
// the GUI chip), translated, and survives narrow terminals: the model name is
// shortened first.
func TestHeaderMemoryOffBadge(t *testing.T) {
	off := true
	m := New(Options{Language: "pl", MemoryOffFn: func() bool { return off }})
	badge := uilang.Text("pl", "tui.status.memoryOff")
	if badge == "" || badge == "tui.status.memoryOff" {
		t.Fatal("badge untranslated")
	}
	for _, width := range []int{120, 60, 40, 24, 16} {
		m.width = width
		header := m.renderHeader()
		if got := lipgloss.Width(header); got > width {
			t.Fatalf("width %d: header is %d columns", width, got)
		}
		if !strings.Contains(ansi.Strip(header), badge) {
			t.Fatalf("width %d: badge missing: %q", width, ansi.Strip(header))
		}
	}
	// Very narrow: the badge is cut but nothing overflows.
	m.width = 8
	if got := lipgloss.Width(m.renderHeader()); got > 8 {
		t.Fatalf("width 8: header is %d columns", got)
	}

	// Memory on (or no switch wired): the header is exactly as before.
	off = false
	m.width = 80
	withSwitch := m.renderHeader()
	plain := New(Options{Language: "pl"})
	plain.width = 80
	if withSwitch != plain.renderHeader() || strings.Contains(ansi.Strip(withSwitch), badge) {
		t.Fatal("memory on must not change the header")
	}
}

func TestMemoryOffBadgeTranslatedEverywhere(t *testing.T) {
	for _, language := range uilang.Languages() {
		if got := uilang.Text(language.Code, "tui.status.memoryOff"); got == "" || got == "tui.status.memoryOff" {
			t.Fatalf("%s: tui.status.memoryOff missing", language.Code)
		}
	}
}
