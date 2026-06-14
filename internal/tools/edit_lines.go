package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"supercli/internal/fileops"
)

// EditLines applies SEVERAL content-anchored edits to one file in
// a single call. It is the batch sibling of edit_line: each edit
// carries its own expected_old proof, every anchor is validated
// before anything is written (all-or-nothing), and edits are
// applied bottom-to-top so an earlier change never shifts the
// line numbers of a later one (ROADMAP edit-format #4).
//
// Schema:
//
//	{
//	  "file":  string (required) — file path
//	  "edits": array  (required) — list of {line, expected_old, new_content}
//	}
//
// Each edit:
//
//	{
//	  "line":         int    — line to edit (1-based); a hint
//	  "expected_old": string — current line content, verbatim (proof)
//	  "new_content":  string — replacement content
//	}
//
// A mismatch on ANY anchor fails the whole call and leaves the
// file untouched. Use this when you have multiple edits to one
// file; use edit_line for a single change.
//
// Verification: "file_write" family (file must exist).
type EditLines struct {
	BaseDir string
}

func NewEditLines(baseDir string) *EditLines {
	return &EditLines{BaseDir: baseDir}
}

type editLinesEdit struct {
	Line        int    `json:"line"`
	ExpectedOld string `json:"expected_old"`
	NewContent  string `json:"new_content"`
}

type editLinesArgs struct {
	File  string          `json:"file"`
	Edits []editLinesEdit `json:"edits"`
}

func (t *EditLines) Spec() Tool {
	return Tool{
		Name:        "edit_lines",
		Description: "Apply several content-anchored edits to one file at once. Each edit gives line (a hint), expected_old (current line content, verbatim — the proof) and new_content. Edits are validated against the file and applied bottom-to-top; any mismatch fails the whole call and leaves the file untouched. Use edit_line for a single change. Use read_context first.",
		Schema: `{
			"file":  {"type": "string", "description": "File path"},
			"edits": {"type": "array", "description": "Edits to apply; each {line, expected_old, new_content}", "items": {"type": "object", "properties": {
				"line":         {"type": "integer", "description": "Line number (1-based), a hint"},
				"expected_old": {"type": "string", "description": "Current content of the line, verbatim (proof)"},
				"new_content":  {"type": "string", "description": "New content for the line"}
			}}}
		}`,
		Fn: t.execute,
	}
}

func (t *EditLines) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a editLinesArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("edit_lines: bad args: %w", err)}, nil
	}
	if len(a.Edits) == 0 {
		return Result{Err: fmt.Errorf("edit_lines: edits is empty (give at least one {line, expected_old, new_content})")}, nil
	}
	edits := make([]fileops.AnchoredEdit, len(a.Edits))
	for i, e := range a.Edits {
		if e.ExpectedOld == "" {
			return Result{Err: fmt.Errorf("edit_lines: edit %d missing expected_old (the verbatim current line is required as proof)", i+1)}, nil
		}
		edits[i] = fileops.AnchoredEdit{Line: e.Line, ExpectedOld: e.ExpectedOld, NewContent: e.NewContent}
	}
	full := t.resolvePath(a.File)
	diff, err := fileops.EditLinesAnchored(full, edits)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_lines: %w", err)}, nil
	}
	return Result{Text: fmt.Sprintf("Edited %d lines (anchored) in %s:\n%s", len(edits), a.File, diff)}, nil
}

func (t *EditLines) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(t.BaseDir, path)
}
