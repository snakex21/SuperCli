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

	"supercli/internal/ui/theme"
)

// adaptive turns a shared design token into a lipgloss colour.
func adaptive(p theme.Pair) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: p.Light, Dark: p.Dark}
}

// Theme colors. Adaptive: the first value targets light
// terminals, the second dark terminals. Renderers must never
// hardcode color codes — add a style here instead.
//
// Shared values come from internal/ui/theme (checked against app.css by
// theme.TestGUITokensMatchSharedTokens), so both interfaces share one
// identity. lipgloss maps them to the nearest 256/16 colour on terminals
// without true colour (classic cmd/conhost).
var (
	// colorAccent is the brand color (GUI --accent; darker on light
	// backgrounds so it stays readable).
	colorAccent = adaptive(theme.Default.AccentFill)
	// colorAccentSoft is a lighter companion accent.
	colorAccentSoft = lipgloss.AdaptiveColor{Light: "#b5520a", Dark: "#f5a97f"}
	// colorInfo is the secondary accent used for user labels
	// and interactive highlights (GUI --hl-fn blue).
	colorInfo = lipgloss.AdaptiveColor{Light: "#1e66f5", Dark: "#82aaff"}
	// colorText is the main body text (GUI --text).
	colorText = adaptive(theme.Default.Text)
	// colorMuted is for secondary text (GUI --muted).
	colorMuted = adaptive(theme.Default.Muted)
	// colorFaint is for decorative lines only (rules, separators). It is
	// below 3:1 on purpose; component borders use colorBorderUI.
	colorFaint = lipgloss.AdaptiveColor{Light: "#c8c8ce", Dark: "#3a3a44"}
	// colorSurface is the raised background of bars and selections
	// (GUI --raise).
	// colorBorderUI outlines components (input box, panels, approval card):
	// >= 3:1 on both terminal backgrounds (WCAG 1.4.11).
	colorBorderUI = adaptive(theme.Default.LineStrong)
	colorSurface  = lipgloss.AdaptiveColor{Light: "#ececf0", Dark: "#1d1d23"}
	// colorSelect is a selected row, one step above the surface.
	colorSelect = lipgloss.AdaptiveColor{Light: "#e2e2e8", Dark: "#2a2a33"}
	// colorCode is inline code (GUI --hl-attr).
	colorCode = lipgloss.AdaptiveColor{Light: "#0b7285", Dark: "#89ddff"}
	// colorSuccess / colorError / colorWarn are status colors
	// (GUI --hl-add, --hl-del and --hl-type).
	colorSuccess = adaptive(theme.Default.Status.OK)
	colorError   = adaptive(theme.Default.Status.Error)
	colorWarn    = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#e5c07b"}
	// colorPending marks a run blocked on the user (approval card).
	colorPending = adaptive(theme.Default.Status.Pending)
	colorDenied  = adaptive(theme.Default.Status.Denied)
	colorRunning = adaptive(theme.Default.Status.Running)
	colorDiffAdd = adaptive(theme.Default.Diff.Add)
	colorDiffDel = adaptive(theme.Default.Diff.Del)
	colorHunk    = adaptive(theme.Default.Diff.Hunk)
)

// Palette is the set of styles used across the TUI. Exported
// so individual renderers (chat, status, markers) can reference
// them without hardcoding values.
type Palette struct {
	// renderer records the color environment used by cached chrome.
	renderer *lipgloss.Renderer
	// Chrome
	Header       lipgloss.Style
	HeaderDim    lipgloss.Style
	HeaderMode   lipgloss.Style
	InputHint    lipgloss.Style
	InputPrompt  lipgloss.Style
	InputText    lipgloss.Style
	Rule         lipgloss.Style
	Panel        lipgloss.Style
	PanelTitle   lipgloss.Style
	PanelMuted   lipgloss.Style
	MenuSelected lipgloss.Style
	MenuTab      lipgloss.Style
	// Bar is the background of the header bar; segment styles inherit it.
	Bar lipgloss.Style

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

	// Markdown blocks: H1 heading, quote bar, fenced-code frame, table rules.
	MdH1    lipgloss.Style
	MdQuote lipgloss.Style
	MdFrame lipgloss.Style

	// Status (shared tokens): colour encodes state.
	StatusPending lipgloss.Style
	StatusDenied  lipgloss.Style
	StatusRunning lipgloss.Style
	// BorderUI is a component border (>= 3:1).
	BorderUI lipgloss.Style

	// Diff lines
	DiffAdd  lipgloss.Style
	DiffDel  lipgloss.Style
	DiffHunk lipgloss.Style
}

// NewPalette builds a palette from a lipgloss Renderer.
// When the renderer uses termenv.Ascii (no color), all
// styles render as plain text — no ANSI escapes.
func NewPalette(r *lipgloss.Renderer) Palette {
	return Palette{
		renderer: r,
		// Chrome
		Header:       r.NewStyle().Foreground(colorAccent).Bold(true),
		HeaderDim:    r.NewStyle().Foreground(colorMuted),
		HeaderMode:   r.NewStyle().Foreground(colorAccentSoft).Bold(true),
		InputHint:    r.NewStyle().Foreground(colorMuted),
		InputPrompt:  r.NewStyle().Foreground(colorAccent).Bold(true),
		InputText:    r.NewStyle().Foreground(colorText),
		Rule:         r.NewStyle().Foreground(colorFaint),
		Panel:        r.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorBorderUI).Padding(1, 2),
		PanelTitle:   r.NewStyle().Foreground(colorAccent).Bold(true),
		PanelMuted:   r.NewStyle().Foreground(colorMuted),
		MenuSelected: r.NewStyle().Foreground(colorAccentSoft).Background(colorSelect).Bold(true),
		MenuTab:      r.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#101014"}).Background(colorAccent).Bold(true),
		Bar:          r.NewStyle().Background(colorSurface),

		// Input area border
		InputBorderFocused: r.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).Padding(0, 1),
		InputBorderBlurred: r.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorBorderUI).Padding(0, 1),

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
		ToolName:   r.NewStyle().Foreground(colorInfo).Bold(true),
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
		MdCode: r.NewStyle().Foreground(colorCode),

		// Thinking blocks
		MdThinking:       r.NewStyle().Foreground(colorMuted).Italic(true),
		MdThinkingHeader: r.NewStyle().Foreground(colorMuted).Bold(true),

		MdH1:    r.NewStyle().Foreground(colorAccent).Bold(true),
		MdQuote: r.NewStyle().Foreground(colorMuted).Italic(true),
		MdFrame: r.NewStyle().Foreground(colorBorderUI),

		StatusPending: r.NewStyle().Foreground(colorPending).Bold(true),
		StatusDenied:  r.NewStyle().Foreground(colorDenied),
		StatusRunning: r.NewStyle().Foreground(colorRunning),
		BorderUI:      r.NewStyle().Foreground(colorBorderUI),

		DiffAdd:  r.NewStyle().Foreground(colorDiffAdd),
		DiffDel:  r.NewStyle().Foreground(colorDiffDel),
		DiffHunk: r.NewStyle().Foreground(colorHunk),
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
	r.SetColorProfile(termenv.Ascii)
	return NewPalette(r)
}
