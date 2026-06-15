// Package tui — theme.go is the single source of truth for the
// TUI's visual identity: one adaptive color palette (safe on
// both light and dark terminals) and the lipgloss styles every
// renderer (chat, status, markers, menus, onboarding) uses.
// Use --no-color to disable ANSI codes.
package tui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Theme colors. Adaptive: the first value targets light
// terminals, the second dark terminals. Renderers must never
// hardcode color codes — add a style here instead.
var (
	// colorAccent is the brand color (warm orange).
	colorAccent = lipgloss.AdaptiveColor{Light: "166", Dark: "209"}
	// colorAccentSoft is a lighter companion accent.
	colorAccentSoft = lipgloss.AdaptiveColor{Light: "172", Dark: "216"}
	// colorInfo is the secondary accent used for user labels
	// and interactive highlights (cool cyan/blue).
	colorInfo = lipgloss.AdaptiveColor{Light: "31", Dark: "81"}
	// colorText is the main body text.
	colorText = lipgloss.AdaptiveColor{Light: "235", Dark: "252"}
	// colorMuted is for secondary text (hints, dim labels).
	colorMuted = lipgloss.AdaptiveColor{Light: "245", Dark: "245"}
	// colorFaint is for chrome lines (rules, borders, separators).
	colorFaint = lipgloss.AdaptiveColor{Light: "250", Dark: "238"}
	// colorSuccess / colorError / colorWarn are status colors.
	colorSuccess = lipgloss.AdaptiveColor{Light: "28", Dark: "78"}
	colorError   = lipgloss.AdaptiveColor{Light: "124", Dark: "203"}
	colorWarn    = lipgloss.AdaptiveColor{Light: "130", Dark: "222"}
)

// Palette is the set of styles used across the TUI. Exported
// so individual renderers (chat, status, markers) can reference
// them without hardcoding values.
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

	// Input area border: accent when focused, faint when not.
	InputBorderFocused lipgloss.Style
	InputBorderBlurred lipgloss.Style

	// User/assistant message roles
	User           lipgloss.Style
	UserLabel      lipgloss.Style
	UserGutter     lipgloss.Style // colored left bar for user blocks
	Assistant      lipgloss.Style
	AssistantLabel lipgloss.Style
	AssistGutter   lipgloss.Style // colored left bar for assistant blocks
	System         lipgloss.Style

	// Inline markers
	Marker    lipgloss.Style
	MarkerDim lipgloss.Style

	// Status bar
	StatusKey   lipgloss.Style
	StatusValue lipgloss.Style
	StatusSep   lipgloss.Style
	StatusDim   lipgloss.Style

	// Tool chips
	ToolName   lipgloss.Style
	ToolOutput lipgloss.Style
	ToolErr    lipgloss.Style

	// Misc
	Success lipgloss.Style
	Error   lipgloss.Style
	Dim     lipgloss.Style
	Bold    lipgloss.Style

	// Markdown
	MdH2   lipgloss.Style // ## heading
	MdH3   lipgloss.Style // ### subheading
	MdCode lipgloss.Style // `code`

	// Thinking blocks
	MdThinking       lipgloss.Style // <thinking> text: muted
	MdThinkingHeader lipgloss.Style // 💭 Thinking: prefix
}

// NewPalette builds a palette from a lipgloss Renderer.
// When the renderer uses termenv.Ascii (no color), all
// styles render as plain text — no ANSI escapes.
func NewPalette(r *lipgloss.Renderer) Palette {
	return Palette{
		// Chrome
		Header:      r.NewStyle().Foreground(colorAccent).Bold(true),
		HeaderDim:   r.NewStyle().Foreground(colorMuted),
		HeaderMode:  r.NewStyle().Foreground(colorAccentSoft).Bold(true),
		InputHint:   r.NewStyle().Foreground(colorMuted),
		InputPrompt: r.NewStyle().Foreground(colorAccent).Bold(true),
		InputText:   r.NewStyle().Foreground(colorText),
		Rule:        r.NewStyle().Foreground(colorFaint),
		Panel:       r.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorFaint).Padding(1, 2),
		PanelTitle:  r.NewStyle().Foreground(colorAccent).Bold(true),
		PanelMuted:  r.NewStyle().Foreground(colorMuted),

		// Input area border
		InputBorderFocused: r.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).Padding(0, 1),
		InputBorderBlurred: r.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorFaint).Padding(0, 1),

		// Messages
		User:           r.NewStyle().Foreground(colorText),
		UserLabel:      r.NewStyle().Foreground(colorInfo).Bold(true),
		UserGutter:     r.NewStyle().Foreground(colorInfo),
		Assistant:      r.NewStyle().Foreground(colorText),
		AssistantLabel: r.NewStyle().Foreground(colorAccent).Bold(true),
		AssistGutter:   r.NewStyle().Foreground(colorAccent),
		System:         r.NewStyle().Foreground(colorWarn),

		// Markers
		Marker:    r.NewStyle().Foreground(colorAccent).Bold(true),
		MarkerDim: r.NewStyle().Foreground(colorMuted),

		// Status bar
		StatusKey:   r.NewStyle().Foreground(colorAccentSoft).Bold(true),
		StatusValue: r.NewStyle().Foreground(colorText),
		StatusSep:   r.NewStyle().Foreground(colorFaint),
		StatusDim:   r.NewStyle().Foreground(colorMuted),

		// Tool chips
		ToolName:   r.NewStyle().Foreground(colorAccent).Bold(true),
		ToolOutput: r.NewStyle().Foreground(colorMuted),
		ToolErr:    r.NewStyle().Foreground(colorError),

		// Misc
		Success: r.NewStyle().Foreground(colorSuccess),
		Error:   r.NewStyle().Foreground(colorError),
		Dim:     r.NewStyle().Foreground(colorMuted),
		Bold:    r.NewStyle().Bold(true),

		// Markdown
		MdH2:   r.NewStyle().Foreground(colorAccentSoft).Bold(true),
		MdH3:   r.NewStyle().Foreground(colorAccent).Bold(true),
		MdCode: r.NewStyle().Foreground(colorSuccess),

		// Thinking blocks
		MdThinking:       r.NewStyle().Foreground(colorMuted),
		MdThinkingHeader: r.NewStyle().Foreground(colorMuted).Italic(true),
	}
}

// DefaultPalette returns the adaptive palette with auto-detected
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
