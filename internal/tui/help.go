package tui

// help.go is a thin re-export of the slash infrastructure.
// All types and functions live in slash.go to avoid duplicates.
// This file exists only for backward-compatible imports from
// tests and other packages that reference "help" symbols.

// RenderHelp returns the full help text. It delegates to
// HelpContent() in slash.go.
func RenderHelp() string {
	return HelpContent()
}
