// Package theme holds the design tokens shared by the TUI and the web GUI.
//
// The TUI palette (internal/ui/tui/view_theme.go) takes its colours from
// here, and tokens_test.go checks that webgui/assets/app.css declares the
// same values for its custom properties, so the two front ends cannot drift.
package theme

// Pair is one token in both themes: Light for light terminals/themes, Dark
// for dark ones (the lipgloss.AdaptiveColor order).
type Pair struct {
	Light, Dark string
}

// Status colours encode state, never the kind of tool (UI audit 1.0.25, 3.1).
type Status struct {
	Running, OK, Warn, Error, Pending, Denied Pair
}

// Diff colours for added/removed lines and hunk headers.
type Diff struct {
	Add, Del, Hunk Pair
}

// Tokens is the shared palette. CSS names are given next to each field.
type Tokens struct {
	Text       Pair // --text
	Muted      Pair // --muted
	LineStrong Pair // --line-strong: component borders, >= 3:1
	Accent     Pair // --accent (TUI keeps its own light value, see view_theme.go)
	AccentFill Pair // --accent-fill
	OnAccent   Pair // --on-accent
	Status     Status
	Diff       Diff
}

// Default is the single source of truth for colours used by both UIs.
var Default = Tokens{
	Text:       Pair{Light: "#1c1c20", Dark: "#e8e8ea"},
	Muted:      Pair{Light: "#63636b", Dark: "#9a9aa2"},
	LineStrong: Pair{Light: "#8e8e98", Dark: "#666673"},
	Accent:     Pair{Light: "#ad4d1a", Dark: "#e8763d"},
	AccentFill: Pair{Light: "#c2531f", Dark: "#e8763d"},
	OnAccent:   Pair{Light: "#ffffff", Dark: "#101014"},
	Status: Status{
		Running: Pair{Light: "#1e66f5", Dark: "#82aaff"},
		OK:      Pair{Light: "#1f7a35", Dark: "#7fd18b"},
		Warn:    Pair{Light: "#8a5a00", Dark: "#e5c07b"},
		Error:   Pair{Light: "#b42318", Dark: "#f08080"},
		Pending: Pair{Light: "#8a5a00", Dark: "#e5c07b"},
		Denied:  Pair{Light: "#8c4a4a", Dark: "#c08a8a"},
	},
	Diff: Diff{
		Add:  Pair{Light: "#1f7a35", Dark: "#7fd18b"},
		Del:  Pair{Light: "#b42318", Dark: "#f08080"},
		Hunk: Pair{Light: "#1e66f5", Dark: "#82aaff"},
	},
}

// CSSVars maps app.css custom properties to their token, for the drift test.
func (t Tokens) CSSVars() map[string]Pair {
	return map[string]Pair{
		"--text": t.Text, "--muted": t.Muted, "--line-strong": t.LineStrong,
		"--accent": t.Accent, "--accent-fill": t.AccentFill, "--on-accent": t.OnAccent,
		"--status-running": t.Status.Running, "--status-ok": t.Status.OK, "--status-warn": t.Status.Warn,
		"--status-error": t.Status.Error, "--status-pending": t.Status.Pending, "--status-denied": t.Status.Denied,
		"--hl-add": t.Diff.Add, "--hl-del": t.Diff.Del, "--hl-hunk": t.Diff.Hunk,
	}
}
