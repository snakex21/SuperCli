package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"supercli/internal/fileops"
)

// EditLine is the F24 tool for replacing exactly one line.
// Returns a diff (±3 context lines) for verification.
//
// Schema:
//
//	{
//	  "file":         string (required) — file path
//	  "line":         int    (required) — line to replace (1-based);
//	                                       a hint when expected_old is set
//	  "new_content":  string (required) — replacement content
//	  "expected_old": string (optional) — current line content, verbatim;
//	                                       when set, the edit is content-anchored
//	}
//
// When expected_old is set the tool delegates to
// fileops.EditLineAnchored: the line number is treated as a
// hint, the edit is validated against the content, small line
// drift is tolerated, and a mismatch fails loudly instead of
// corrupting the file. When expected_old is empty the legacy
// line-only behaviour (fileops.EditLine) is used.
//
// Verification: "file_write" family (file must exist).
type EditLine struct {
	BaseDir string
}

func NewEditLine(baseDir string) *EditLine {
	return &EditLine{BaseDir: baseDir}
}

type editLineArgs struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	NewContent string `json:"new_content"`
	// ExpectedOld, when non-empty, switches edit_line to the
	// content-anchored path: `line` becomes a hint and this
	// verbatim content is the proof of WHERE to edit. The tool
	// fails loudly instead of corrupting the file when the
	// content does not match near the hint.
	ExpectedOld string `json:"expected_old"`
}

func (t *EditLine) Spec() Tool {
	return Tool{
		Name:        "edit_line",
		Description: "Replace one line in a file. Prefer passing expected_old (the current line content, verbatim): then line is just a hint and the edit is validated against the content — it self-corrects small line drift and fails loudly instead of corrupting the file. Returns a diff for verification. Use read_context first.",
		Schema: `{
			"file":         {"type": "string", "description": "File path"},
			"line":         {"type": "integer", "description": "Line number (1-based). A hint when expected_old is set."},
			"new_content":  {"type": "string", "description": "New content for the line"},
			"expected_old": {"type": "string", "description": "Current content of the line, verbatim. When set, the edit is anchored to this content (recommended): line drift is tolerated and a mismatch fails instead of corrupting the file."}
		}`,
		Fn: t.execute,
	}
}

func (t *EditLine) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a editLineArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("edit_line: bad args: %w", err)}, nil
	}
	full := t.resolvePath(a.File)
	// Anchored path when the model supplied proof content.
	if a.ExpectedOld != "" {
		diff, err := fileops.EditLineAnchored(full, a.Line, a.ExpectedOld, a.NewContent)
		if err != nil {
			return Result{Err: fmt.Errorf("edit_line: %w", err)}, nil
		}
		return Result{Text: fmt.Sprintf("Edited (anchored) near line %d in %s:\n%s", a.Line, a.File, diff)}, nil
	}
	diff, err := fileops.EditLine(full, a.Line, a.NewContent)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_line: %w", err)}, nil
	}
	return Result{Text: fmt.Sprintf("Edited line %d in %s:\n%s", a.Line, a.File, diff)}, nil
}

func (t *EditLine) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(t.BaseDir, path)
}
