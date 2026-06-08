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
//	  "file":        string (required) — file path
//	  "line":        int    (required) — line to replace (1-based)
//	  "new_content": string (required) — replacement content
//	}
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
}

func (t *EditLine) Spec() Tool {
	return Tool{
		Name:        "edit_line",
		Description: "Replace exactly one line in a file. Returns a diff for verification. Use read_context first to find the right line.",
		Schema: `{
			"file":        {"type": "string", "description": "File path"},
			"line":        {"type": "integer", "description": "Line number to replace (1-based)"},
			"new_content": {"type": "string", "description": "New content for the line"}
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
