package files

import (
	"context"
	"encoding/json"
	"fmt"

	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

// DeleteLines is the F24 tool for removing a range of lines.
//
// Schema:
//
//	{
//	  "file": string (required) — file path
//	  "from": int    (required) — start line (1-based, inclusive)
//	  "to":   int    (required) — end line (1-based, inclusive)
//	}
//
// Verification: "file_write" family (file must exist).
type DeleteLines struct {
	BaseDir string
}

func NewDeleteLines(baseDir string) *DeleteLines {
	return &DeleteLines{BaseDir: baseDir}
}

type deleteLinesArgs struct {
	File string `json:"file"`
	From int    `json:"from"`
	To   int    `json:"to"`
}

func (t *DeleteLines) Spec() Tool {
	return Tool{
		Name:        "delete_lines",
		Description: "Delete a range of lines from a file (1-based, inclusive). Returns a diff for verification.",
		Schema: `{
			"file": {"type": "string", "description": "File path"},
			"from": {"type": "integer", "description": "Start line (1-based, inclusive)"},
			"to":   {"type": "integer", "description": "End line (1-based, inclusive)"}
		}`,
		Fn: t.execute,
	}
}

func (t *DeleteLines) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a deleteLinesArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("delete_lines: bad args: %w", err)}, nil
	}
	full, err := t.resolvePath(a.File)
	if err != nil {
		return Result{Err: fmt.Errorf("delete_lines: %w", err)}, nil
	}
	diff, err := fileops.DeleteLines(full, a.From, a.To)
	if err != nil {
		return Result{Err: fmt.Errorf("delete_lines: %w", err)}, nil
	}
	return Result{Text: fmt.Sprintf("Deleted lines %d-%d in %s:\n%s", a.From, a.To, a.File, diff)}, nil
}

// resolvePath resolves file against BaseDir through the sandbox so
// the model cannot delete lines outside the project home. Mirrors write_file.
func (t *DeleteLines) resolvePath(path string) (string, error) {
	return sandbox.ResolveSafe(t.BaseDir, path)
}
