package files

import (
	"context"
	"encoding/json"
	"fmt"

	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

// WriteFile is the thin tool for creating or overwriting a file
// with given content. It is the counterpart the model reached for
// (and could not find) when asked to "create a file": the older
// file_ops tool had no create-with-content action, so a small
// model burned its whole step budget guessing. write_file gives it
// one obvious, single-purpose tool.
//
// Schema:
//
//	{
//	  "path":    string (required) — file path (relative to home)
//	  "content": string (required) — full file contents
//	}
//
// Safety: the path is resolved with sandbox.ResolveSafe against
// BaseDir, so the model cannot write outside the project home —
// the same boundary ctx_execute enforces. Parent directories are
// created automatically.
//
// Verification: "file_write" family.
type WriteFile struct {
	BaseDir string
}

// NewWriteFile returns a WriteFile tool rooted at baseDir.
func NewWriteFile(baseDir string) *WriteFile {
	return &WriteFile{BaseDir: baseDir}
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Spec returns the tool definition.
func (t *WriteFile) Spec() Tool {
	return Tool{
		Name:        "write_file",
		Description: "Create a new file (or overwrite an existing one) with the given content. Creates parent folders as needed. Use this to make a file from scratch; use patch_file to change an existing one.",
		Schema: `{
			"path":    {"type": "string", "description": "File path, relative to the project folder"},
			"content": {"type": "string", "description": "Full contents to write to the file"}
		}`,
		Fn: t.execute,
	}
}

func (t *WriteFile) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a writeFileArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("write_file: bad args: %w", err)}, nil
	}
	if a.Path == "" {
		return Result{Err: fmt.Errorf("write_file: path is required")}, nil
	}
	full, err := sandbox.ResolveSafe(t.BaseDir, a.Path)
	if err != nil {
		return Result{Err: fmt.Errorf("write_file: %w", err)}, nil
	}
	res, err := fileops.WriteFile(full, a.Content)
	if err != nil {
		return Result{Err: fmt.Errorf("write_file: %w", err)}, nil
	}
	verb := "Overwrote"
	if res.Created {
		verb = "Created"
	}
	return Result{Text: fmt.Sprintf("%s %s (%d bytes)", verb, a.Path, res.Bytes)}, nil
}
