package files

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

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
//	  "from": int    (optional) — start line (1-based; default 1)
//	  "to":   int    (optional) — inclusive end; default bounded range
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

const defaultReadLinesRange = 300

type readLinesArgs struct {
	File string `json:"file"`
	From int    `json:"from"`
	To   *int   `json:"to"`
}

func (t *ReadLines) Spec() Tool {
	return Tool{
		Name:        "read_lines",
		Description: "Read numbered file lines. Start defaults to 1; missing end reads up to 300 lines. Explicit ranges: max 500.",
		ReadOnly:    true,
		Schema: `{
			"file": {"type": "string", "description": "File path (relative or absolute)"},
			"from": {"type": "integer", "description": "Start line (1-based; default 1)"},
			"to":   {"type": "integer", "description": "End line (inclusive; optional)"}
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
	// A file-only read is unambiguous. Bound it like a bare read_many entry
	// rather than spending a model turn repairing an omitted end line.
	end := math.MaxInt
	if a.To != nil {
		end = *a.To
	} else if a.From <= math.MaxInt-defaultReadLinesRange+1 {
		end = a.From + defaultReadLinesRange - 1
	}
	// Keep useful bounded evidence when a model overestimates the range (often
	// by one inclusive line). The library stays strict, and unread requested
	// lines are reported below instead of silently disappearing.
	requestedTo := end
	if end >= a.From && end-a.From >= fileops.MaxLineRange {
		end = a.From + fileops.MaxLineRange - 1
	}
	full, err := resolveSandboxed(t.BaseDir, a.File)
	if err != nil {
		return Result{Err: fmt.Errorf("read_lines: %w", err)}, nil
	}
	lines, eof, err := fileops.ReadLinesBoundedWithEOF(ctx, full, a.From, end, maxReadLineKeep)
	if err != nil {
		return Result{Err: fmt.Errorf("read_lines: %w", suggestReadFile(ctx, full, err))}, nil
	}
	text := renderLinesWithEOF(lines, eof)
	if end < requestedTo && !eof {
		text += fmt.Sprintf("[range capped at %d lines; requested lines %d-%d not read]\n",
			fileops.MaxLineRange, end+1, requestedTo)
	}
	if a.To == nil && !eof && end < math.MaxInt {
		text += fmt.Sprintf("[default range: lines %d-%d; continue from line %d]\n", a.From, end, end+1)
	}
	return Result{Text: text}, nil
}
