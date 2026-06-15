package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

// InsertAfter is the F24 tool for inserting a new line
// after a specified line.
//
// Schema:
//
//	{
//	  "file":    string (required) — file path
//	  "line":    int    (required) — insert after this line (1-based)
//	  "content": string (required) — new line content
//	}
//
// Verification: "file_write" family (file must exist).
type InsertAfter struct {
	BaseDir string
}

func NewInsertAfter(baseDir string) *InsertAfter {
	return &InsertAfter{BaseDir: baseDir}
}

type insertAfterArgs struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

func (t *InsertAfter) Spec() Tool {
	return Tool{
		Name:        "insert_after",
		Description: "Insert a new line after a specified line number. Returns a diff for verification.",
		Schema: `{
			"file":    {"type": "string", "description": "File path"},
			"line":    {"type": "integer", "description": "Insert after this line (1-based)"},
			"content": {"type": "string", "description": "New line content"}
		}`,
		Fn: t.execute,
	}
}

func (t *InsertAfter) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a insertAfterArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("insert_after: bad args: %w", err)}, nil
	}
	full, err := t.resolvePath(a.File)
	if err != nil {
		return Result{Err: fmt.Errorf("insert_after: %w", err)}, nil
	}
	diff, err := fileops.InsertAfter(full, a.Line, a.Content)
	if err != nil {
		return Result{Err: fmt.Errorf("insert_after: %w", err)}, nil
	}
	return Result{Text: fmt.Sprintf("Inserted after line %d in %s:\n%s", a.Line, a.File, diff)}, nil
}

// resolvePath resolves file against BaseDir through the sandbox so
// the model cannot insert outside the project home. Mirrors write_file.
func (t *InsertAfter) resolvePath(path string) (string, error) {
	return sandbox.ResolveSafe(t.BaseDir, path)
}
