// Package tui is the Bubble Tea presentation layer. F25 replaces
// the raw transcript with a structured chat view (role-based
// colors), adds a status bar, inline event markers, a tool-
// name spinner, Ctrl+C run cancellation, and PgUp/PgDn scrolling.
package tui

var localSlashCommands = map[string]bool{
	"help":      true,
	"memory":    true,
	"status":    true,
	"sandbox":   true,
	"allow-all": true,
	"clear":     true,
	"reflect":   true,
	"resume":    true,
	"workers":   true,
	"context":   true,
}
