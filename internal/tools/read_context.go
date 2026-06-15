package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"supercli/internal/tools/fileops"
)

// ReadContext is the F24 tool for reading N lines around
// a specific line. Useful for understanding context before
// editing.
//
// Schema:
//
//	{
//	  "file":   string (required) — file path
//	  "line":   int    (required) — target line (1-based)
//	  "radius": int    (optional) — ±N lines, default 10
//	}
//
// Verification: "read" family (non-empty text).
type ReadContext struct {
	BaseDir string
}

func NewReadContext(baseDir string) *ReadContext {
	return &ReadContext{BaseDir: baseDir}
}

type readContextArgs struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Radius int    `json:"radius"`
}

func (t *ReadContext) Spec() Tool {
	return Tool{
		Name:        "read_context",
		Description: "Read N lines around a target line (default ±10). Use before edit_line to understand context.",
		Schema: `{
			"file":   {"type": "string", "description": "File path"},
			"line":   {"type": "integer", "description": "Target line (1-based)"},
			"radius": {"type": "integer", "description": "±N lines (default 10)"}
		}`,
		Fn: t.execute,
	}
}

func (t *ReadContext) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a readContextArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("read_context: bad args: %w", err)}, nil
	}
	full := t.resolvePath(a.File)
	lines, err := fileops.ReadContext(full, a.Line, a.Radius)
	if err != nil {
		return Result{Err: fmt.Errorf("read_context: %w", err)}, nil
	}
	var text string
	for _, l := range lines {
		text += fmt.Sprintf("%4d | %s\n", l.Number, l.Content)
	}
	return Result{Text: text}, nil
}

func (t *ReadContext) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(t.BaseDir, path)
}
