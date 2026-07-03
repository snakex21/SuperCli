package files

import (
	"context"
	"encoding/json"
	"fmt"

	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
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
		Description: "Replace one line in a file. Strongly prefer passing expected_old (the current line, verbatim): then line is only a hint, the edit is validated against the content, self-corrects small drift, and fails loudly instead of corrupting the file. Run read_context first. Returns a diff.",
		Schema: `{"file":{"type":"string","description":"File path"},
"line":{"type":"integer","description":"1-based line; a hint when expected_old is set"},
"new_content":{"type":"string","description":"Replacement line content"},
"expected_old":{"type":"string","description":"Current line verbatim (recommended): anchors the edit, tolerates drift, fails instead of corrupting"}}`,
		Fn: t.execute,
	}
}

func (t *EditLine) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a editLineArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("edit_line: bad args: %w", err)}, nil
	}
	full, err := t.resolvePath(a.File)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_line: %w", err)}, nil
	}
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

// resolvePath resolves file against BaseDir through the sandbox so
// the model cannot edit outside the project home — absolute paths
// and .. traversal that escape are rejected. Mirrors write_file.
func (t *EditLine) resolvePath(path string) (string, error) {
	return sandbox.ResolveSafe(t.BaseDir, path)
}
