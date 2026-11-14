package tui

import (
	"regexp"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// ansiColorSGR matches SGR sequences that set a foreground/background
// colour (30-37, 39, 40-47, 49, 90-97, 100-107, 38;..., 48;...).
var ansiColorSGR = regexp.MustCompile(`\x1b\[(?:[0-9]*;)*(?:3[0-9]|4[0-9]|9[0-7]|10[0-7])(?:;[0-9]*)*m`)

// TestNoColorViewHasNoColourEscapes forces the process-wide lipgloss
// renderer to TrueColor so any style that bypasses the palette would
// leak colour, then checks that a NoColor model renders none.
func TestNoColorViewHasNoColourEscapes(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	colour := NewPalette(lipgloss.NewRenderer(nil, termenv.WithProfile(termenv.TrueColor)))
	colour.renderer.SetColorProfile(termenv.TrueColor)
	if !ansiColorSGR.MatchString(colour.Error.Render("x")) {
		t.Fatal("sanity: colour palette rendered no colour escape; regexp is wrong")
	}

	m := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), Language: "en", NoColor: true})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(Model)
	views := map[string]string{
		"view":          m.View(),
		"accounts":      m.renderAccountsMenu(),
		"account label": m.renderAccountLabelMenu(),
		"error style":   m.palette.Error.Render("boom"),
	}
	for name, v := range views {
		if loc := ansiColorSGR.FindStringIndex(v); loc != nil {
			t.Fatalf("%s: NoColor output contains colour escape %q", name, v[loc[0]:loc[1]])
		}
	}
}
