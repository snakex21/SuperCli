package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"supercli/internal/fileops"
)

// ReadLines is the F24 tool for reading specific line
// ranges from a file. The model uses this instead of
// loading the entire file to save tokens.
//
// Schema:
//
//	{
//	  "file": string (required) — file path (relative to home or absolute)
//	  "from": int    (required) — start line (1-based)
//	  "to":   int    (required) — end line (1-based, inclusive)
//	}
//
// Capped at 500 lines per call. Returns lines with
// their 1-based numbers. Verification: "read" family
// (non-empty text).
type ReadLines struct {
	BaseDir string
}

func NewReadLines(baseDir string) *ReadLines {
	return &ReadLines{BaseDir: baseDir}
}

type readLinesArgs struct {
	File string `json:"file"`
	From int    `json:"from"`
	To   int    `json:"to"`
}

func (t *ReadLines) Spec() Tool {
	return Tool{
		Name:        "read_lines",
		Description: "Read a specific range of lines from a file (1-based, inclusive). Max 500 lines. Use instead of file_read for targeted reads.",
		Schema: `{
			"file": {"type": "string", "description": "File path (relative or absolute)"},
			"from": {"type": "integer", "description": "Start line number (1-based)"},
			"to":   {"type": "integer", "description": "End line number (1-based, inclusive)"}
		}`,
		Fn: t.execute,
	}
}

func (t *ReadLines) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a readLinesArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("read_lines: bad args: %w", err)}, nil
	}
	full := t.resolvePath(a.File)
	lines, err := fileops.ReadLines(full, a.From, a.To)
	if err != nil {
		return Result{Err: fmt.Errorf("read_lines: %w", err)}, nil
	}
	var text string
	for _, l := range lines {
		text += fmt.Sprintf("%4d | %s\n", l.Number, l.Content)
	}
	return Result{Text: text}, nil
}

func (t *ReadLines) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(t.BaseDir, path)
}
