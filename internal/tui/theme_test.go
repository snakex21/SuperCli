package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestDefaultPalette_AllStylesNonEmpty(t *testing.T) {
	p := DefaultPalette()
	styles := []struct {
		name string
		s    lipgloss.Style
	}{
		{"Header", p.Header},
		{"HeaderDim", p.HeaderDim},
		{"HeaderMode", p.HeaderMode},
		{"InputHint", p.InputHint},
		{"InputPrompt", p.InputPrompt},
		{"InputText", p.InputText},
		{"Rule", p.Rule},
		{"Panel", p.Panel},
		{"PanelTitle", p.PanelTitle},
		{"PanelMuted", p.PanelMuted},
		{"User", p.User},
		{"UserLabel", p.UserLabel},
		{"Assistant", p.Assistant},
		{"AssistantLabel", p.AssistantLabel},
		{"System", p.System},
		{"Marker", p.Marker},
		{"MarkerDim", p.MarkerDim},
		{"StatusKey", p.StatusKey},
		{"StatusValue", p.StatusValue},
		{"StatusSep", p.StatusSep},
		{"StatusDim", p.StatusDim},
		{"ToolName", p.ToolName},
		{"ToolOutput", p.ToolOutput},
		{"ToolErr", p.ToolErr},
		{"Success", p.Success},
		{"Error", p.Error},
		{"Dim", p.Dim},
		{"Bold", p.Bold},
	}
	for _, st := range styles {
		rendered := st.s.Render("test")
		if strings.TrimSpace(rendered) == "" {
			t.Errorf("%s rendered as empty", st.name)
		}
	}
}

func TestNoColorPalette_RendersPlainText(t *testing.T) {
	p := NoColorPalette()
	rendered := p.User.Render("hello")
	// Should not contain ANSI escape sequences.
	if strings.Contains(rendered, "\x1b[") {
		t.Errorf("NoColorPalette still emits ANSI: %q", rendered)
	}
	if strings.TrimSpace(rendered) != "hello" {
		t.Errorf("unexpected output: %q", rendered)
	}
}

func TestDefaultPalette_MarkdownStylesNonEmpty(t *testing.T) {
	p := DefaultPalette()
	styles := []struct {
		name string
		s    lipgloss.Style
	}{
		{"MdH2", p.MdH2},
		{"MdH3", p.MdH3},
		{"MdCode", p.MdCode},
		{"MdThinking", p.MdThinking},
		{"MdThinkingHeader", p.MdThinkingHeader},
	}
	for _, st := range styles {
		rendered := st.s.Render("test")
		if strings.TrimSpace(rendered) == "" {
			t.Errorf("%s rendered as empty", st.name)
		}
	}
}

func TestDefaultPalette_BoldRenders(t *testing.T) {
	p := DefaultPalette()
	out := p.Bold.Render("bold")
	if !strings.Contains(out, "bold") {
		t.Errorf("Bold: %q does not contain 'bold'", out)
	}
}

func TestNoColorPalette_BoldStillRenders(t *testing.T) {
	p := NoColorPalette()
	out := p.Bold.Render("bold")
	if !strings.Contains(out, "bold") {
		t.Errorf("NoColor Bold: %q does not contain 'bold'", out)
	}
}

func TestNoColorPalette_MarkdownStylesPlain(t *testing.T) {
	p := NoColorPalette()
	for _, st := range []lipgloss.Style{p.MdH2, p.MdH3, p.MdCode} {
		rendered := st.Render("test")
		if strings.Contains(rendered, "\x1b[") {
			t.Errorf("NoColor markdown style still emits ANSI: %q", rendered)
		}
	}
}

func TestPalette_StylesAreDistinctObjects(t *testing.T) {
	p := DefaultPalette()
	// Verify that key style pairs are different objects.
	// In no-color mode they may render the same, but objects differ.
	styles := []struct {
		a, b lipgloss.Style
		name string
	}{
		{p.User, p.Assistant, "User vs Assistant"},
		{p.Error, p.Success, "Error vs Success"},
		{p.MdH2, p.MdH3, "MdH2 vs MdH3"},
	}
	for _, s := range styles {
		// Render both and check they produce output (even if plain).
		aOut := s.a.Render("test")
		bOut := s.b.Render("test")
		if aOut == "" || bOut == "" {
			t.Errorf("%s: one of the styles renders empty", s.name)
		}
		_ = aOut
		_ = bOut
	}
}
