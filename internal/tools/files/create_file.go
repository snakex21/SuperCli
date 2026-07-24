package files

import (
	"context"
	"encoding/json"
	"fmt"

	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

// CreateFile creates a brand-new file only (never overwrites).
type CreateFile struct {
	BaseDir string
}

// NewCreateFile returns a CreateFile tool rooted at baseDir.
func NewCreateFile(baseDir string) *CreateFile {
	return &CreateFile{BaseDir: baseDir}
}

type createFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Spec returns the tool definition (full JSON Schema).
func (t *CreateFile) Spec() Tool {
	return Tool{
		Name: "create_file",
		Description: "Create a new file with the given content. Creates parent folders as needed. " +
			"Refuses if the file already exists — use patch_file to edit existing files. " +
			"Do not use tool_search to find a create tool; call create_file directly.",
		Schema: `{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File path relative to the project folder"},
				"content": {"type": "string", "description": "Full contents of the new file"}
			},
			"required": ["path", "content"],
			"additionalProperties": false
		}`,
		Fn: t.execute,
	}
}

func (t *CreateFile) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a createFileArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("create_file: bad args: %w", err)}, nil
	}
	if a.Path == "" {
		return Result{Err: fmt.Errorf("create_file: path is required")}, nil
	}
	full, err := sandbox.ResolveSafe(t.BaseDir, a.Path)
	if err != nil {
		return Result{Err: fmt.Errorf("create_file: %w", err)}, nil
	}
	if err := fileops.CreateFileExclusive(full, a.Content); err != nil {
		return Result{Err: fmt.Errorf("create_file: %w", err)}, nil
	}
	return Result{Text: fmt.Sprintf("Created %s (%d bytes)", a.Path, len(a.Content))}, nil
}
