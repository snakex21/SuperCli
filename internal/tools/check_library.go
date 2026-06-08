package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"supercli/internal/library"
)

// CheckLibraryAlternatives is the F17 opt-in tool. The
// model calls it before committing to a library to
// discover whether a better alternative exists for the
// specific use case.
//
// Schema:
//
//	{
//	  "library": string (required) — library name
//	  "task":    string (required) — use case description
//	}
//
// Result text the model sees is a one-line recommendation
// or a "no known better alternative" message. The tool
// uses the built-in catalog; no network calls.
//
// The tool is registered opt-in (NOT MarkAlwaysOn); the
// model discovers it via tool_search when needed.
type CheckLibraryAlternatives struct {
	// Finder is the F17 library alternatives engine.
	// nil disables the tool (returns friendly text).
	Finder *library.Finder
}

// NewCheckLibraryAlternatives returns a tool bound to finder.
func NewCheckLibraryAlternatives(finder *library.Finder) *CheckLibraryAlternatives {
	return &CheckLibraryAlternatives{Finder: finder}
}

type checkLibraryArgs struct {
	Library string `json:"library"`
	Task    string `json:"task"`
}

// Spec returns the tool registration.
func (t *CheckLibraryAlternatives) Spec() Tool {
	return Tool{
		Name:        "check_library_alternatives",
		Description: "Check if a better alternative exists for a library given a specific use case. Use before committing to a library.",
		Schema: `{
			"library": {"type": "string", "description": "Library name (e.g. \"leaflet\", \"moment.js\")"},
			"task":    {"type": "string", "description": "Use case (e.g. \"10k polygons\", \"tree-shaking\")"}
		}`,
		Fn: t.execute,
	}
}

func (t *CheckLibraryAlternatives) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	if t.Finder == nil {
		return Result{Text: "Library alternatives search is not configured. Proceed with your original choice."}, nil
	}

	var a checkLibraryArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("check_library_alternatives: bad args: %w", err)}, nil
	}
	a.Library = sanitizeInput(a.Library)
	a.Task = sanitizeInput(a.Task)

	if a.Library == "" {
		return Result{Text: "Provide a library name to check."}, nil
	}
	if a.Task == "" {
		return Result{Text: "Provide a use case description so I can find relevant alternatives."}, nil
	}

	r := t.Finder.Check(a.Library, a.Task)
	txt := library.FormatResult(r, a.Library)
	return Result{Text: txt}, nil
}

// sanitizeInput trims whitespace and limits length to
// prevent abuse. The catalog is tiny so even O(n) scans
// are fine; the guard is for sanity, not performance.
func sanitizeInput(s string) string {
	if len(s) > 200 {
		s = s[:200]
	}
	// TrimSpace is in strings; import avoided to keep
	// this file self-contained. Manual trim:
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
