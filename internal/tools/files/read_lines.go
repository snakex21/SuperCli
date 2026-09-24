package files

import (
	"context"
	"encoding/json"
	"fmt"

	"supercli/internal/tools/fileops"
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
		ReadOnly:    true,
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
	// from=0 was 10 of the 14 observed read_lines failures: the model means
	// "start of the file" and lines are 1-based. There is no line 0 to confuse
	// it with, so clamping is unambiguous and cheaper than an error turn. The
	// library contract (fileops.ReadLines) stays strict for other callers.
	if a.From < 1 {
		a.From = 1
	}
	// Keep useful bounded evidence when a model overestimates the range (often
	// by one inclusive line). The library stays strict, and unread requested
	// lines are reported below instead of silently disappearing.
	requestedTo := a.To
	if a.To >= a.From && a.To-a.From >= fileops.MaxLineRange {
		a.To = a.From + fileops.MaxLineRange - 1
	}
	full, err := resolveSandboxed(t.BaseDir, a.File)
	if err != nil {
		return Result{Err: fmt.Errorf("read_lines: %w", err)}, nil
	}
	lines, eof, err := fileops.ReadLinesBoundedWithEOF(ctx, full, a.From, a.To, maxReadLineKeep)
	if err != nil {
		return Result{Err: fmt.Errorf("read_lines: %w", suggestReadFile(ctx, full, err))}, nil
	}
	text := renderLinesWithEOF(lines, eof)
	if a.To < requestedTo && !eof {
		text += fmt.Sprintf("[range capped at %d lines; requested lines %d-%d not read]\n",
			fileops.MaxLineRange, a.To+1, requestedTo)
	}
	return Result{Text: text}, nil
}
