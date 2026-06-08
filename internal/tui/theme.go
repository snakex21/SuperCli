// Package tui — theme.go defines the color palette and lipgloss
// styles for the terminal UI. All colors are tuned for dark
// terminals (default). Use --no-color to disable ANSI codes.
package tui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Palette is the set of colors used across the TUI. Exported
// so individual renderers (chat, status, markers) can reference
// them without hardcoding hex values.
type Palette struct {
	// Chrome
	Header      lipgloss.Style
	HeaderDim   lipgloss.Style
	HeaderMode  lipgloss.Style
	InputHint   lipgloss.Style
	InputPrompt lipgloss.Style
	InputText   lipgloss.Style
	Rule        lipgloss.Style
	Panel       lipgloss.Style
	PanelTitle  lipgloss.Style
	PanelMuted  lipgloss.Style

	// User/assistant message roles
	User           lipgloss.Style
	UserLabel      lipgloss.Style
	Assistant      lipgloss.Style
	AssistantLabel lipgloss.Style
	System         lipgloss.Style

	// Inline markers
	Marker    lipgloss.Style
	MarkerDim lipgloss.Style

	// Status bar
	StatusKey   lipgloss.Style
	StatusValue lipgloss.Style
	StatusSep   lipgloss.Style
	StatusDim   lipgloss.Style

	// Tool spinner
	ToolName   lipgloss.Style
	ToolOutput lipgloss.Style
	ToolErr    lipgloss.Style

	// Misc
	Success lipgloss.Style
	Error   lipgloss.Style
	Dim     lipgloss.Style
	Bold    lipgloss.Style

	// Markdown
	MdH2   lipgloss.Style // ## heading: cyan, bold
	MdH3   lipgloss.Style // ### subheading: yellow, bold
	MdCode lipgloss.Style // `code`: green on dark bg

	// Thinking blocks
	MdThinking       lipgloss.Style // <thinking> text: gray, dim
	MdThinkingHeader lipgloss.Style // 💭 Thinking: prefix
}

// NewPalette builds a palette from a lipgloss Renderer.
// When the renderer uses termenv.Ascii (no color), all
// styles render as plain text — no ANSI escapes.
func NewPalette(r *lipgloss.Renderer) Palette {
	accent := lipgloss.Color("209") // warm Claude-like orange
	accent2 := lipgloss.Color("216")
	muted := lipgloss.Color("245")
	faint := lipgloss.Color("238")
	return Palette{
		// Chrome
		Header:      r.NewStyle().Foreground(accent).Bold(true),
		HeaderDim:   r.NewStyle().Foreground(muted),
		HeaderMode:  r.NewStyle().Foreground(accent2).Bold(true),
		InputHint:   r.NewStyle().Foreground(muted),
		InputPrompt: r.NewStyle().Foreground(accent).Bold(true),
		InputText:   r.NewStyle().Foreground(lipgloss.Color("252")),
		Rule:        r.NewStyle().Foreground(faint),
		Panel:       r.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(faint).Padding(1, 2),
		PanelTitle:  r.NewStyle().Foreground(accent).Bold(true),
		PanelMuted:  r.NewStyle().Foreground(muted),

		// Messages
		User:           r.NewStyle().Foreground(lipgloss.Color("252")),
		UserLabel:      r.NewStyle().Foreground(lipgloss.Color("81")).Bold(true),
		Assistant:      r.NewStyle().Foreground(lipgloss.Color("252")),
		AssistantLabel: r.NewStyle().Foreground(accent).Bold(true),
		System:         r.NewStyle().Foreground(lipgloss.Color("222")),

		// Markers
		Marker:    r.NewStyle().Foreground(accent).Bold(true),
		MarkerDim: r.NewStyle().Foreground(muted),

		// Status bar
		StatusKey:   r.NewStyle().Foreground(accent2).Bold(true),
		StatusValue: r.NewStyle().Foreground(lipgloss.Color("252")),
		StatusSep:   r.NewStyle().Foreground(faint),
		StatusDim:   r.NewStyle().Foreground(muted),

		// Tool spinner
		ToolName:   r.NewStyle().Foreground(accent).Bold(true),
		ToolOutput: r.NewStyle().Foreground(muted),
		ToolErr:    r.NewStyle().Foreground(lipgloss.Color("1")), // red

		// Misc
		Success: r.NewStyle().Foreground(lipgloss.Color("2")), // green
		Error:   r.NewStyle().Foreground(lipgloss.Color("1")), // red
		Dim:     r.NewStyle().Foreground(lipgloss.Color("8")), // gray
		Bold:    r.NewStyle().Bold(true),

		// Markdown
		MdH2:   r.NewStyle().Foreground(accent2).Bold(true),
		MdH3:   r.NewStyle().Foreground(accent).Bold(true),
		MdCode: r.NewStyle().Foreground(lipgloss.Color("2")).Background(lipgloss.Color("0")), // green on black bg

		// Thinking blocks
		MdThinking:       r.NewStyle().Foreground(lipgloss.Color("8")),              // gray dim
		MdThinkingHeader: r.NewStyle().Foreground(lipgloss.Color("8")).Italic(true), // gray italic
	}
}

// DefaultPalette returns the dark-terminal palette with auto-detected
// color profile. Every style renders non-empty.
func DefaultPalette() Palette {
	return NewPalette(lipgloss.NewRenderer(os.Stderr))
}

// NoColorPalette returns a palette where all styles are plain
// text — no ANSI escape codes. Used with --no-color flag.
func NoColorPalette() Palette {
	r := lipgloss.NewRenderer(os.Stderr, termenv.WithProfile(termenv.Ascii))
	return NewPalette(r)
}
