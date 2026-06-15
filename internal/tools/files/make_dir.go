package files

import (
	"context"
	"encoding/json"
	"fmt"

	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

// MakeDir is the thin tool for creating a folder (and any missing
// parents), the directory counterpart to write_file. It replaces
// the create_folder action of the old grab-bag file_ops tool with
// a single-purpose tool the model can pick unambiguously.
//
// Schema:
//
//	{
//	  "path": string (required) — folder path (relative to home)
//	}
//
// Safety: path is resolved with sandbox.ResolveSafe against
// BaseDir, so the model cannot create folders outside the project
// home. Creating an already-existing folder is a no-op success.
//
// Verification: "file_write" family.
type MakeDir struct {
	BaseDir string
}

// NewMakeDir returns a MakeDir tool rooted at baseDir.
func NewMakeDir(baseDir string) *MakeDir {
	return &MakeDir{BaseDir: baseDir}
}

type makeDirArgs struct {
	Path string `json:"path"`
}

// Spec returns the tool definition.
func (t *MakeDir) Spec() Tool {
	return Tool{
		Name:        "make_dir",
		Description: "Create a folder, including any missing parent folders (like mkdir -p). Does nothing if the folder already exists.",
		Schema: `{
			"path": {"type": "string", "description": "Folder path, relative to the project folder"}
		}`,
		Fn: t.execute,
	}
}

func (t *MakeDir) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a makeDirArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("make_dir: bad args: %w", err)}, nil
	}
	if a.Path == "" {
		return Result{Err: fmt.Errorf("make_dir: path is required")}, nil
	}
	full, err := sandbox.ResolveSafe(t.BaseDir, a.Path)
	if err != nil {
		return Result{Err: fmt.Errorf("make_dir: %w", err)}, nil
	}
	created, err := fileops.MakeDir(full)
	if err != nil {
		return Result{Err: fmt.Errorf("make_dir: %w", err)}, nil
	}
	if !created {
		return Result{Text: fmt.Sprintf("Folder %s already exists", a.Path)}, nil
	}
	return Result{Text: fmt.Sprintf("Created folder %s", a.Path)}, nil
}
