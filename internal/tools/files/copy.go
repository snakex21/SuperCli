package files

import (
	"context"
	"encoding/json"
	"fmt"

	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

// Copy is the thin tool that copies a file or folder — one verb
// for both, like `cp` / `cp -r`. Folders are copied recursively.
// Replaces the copy action of the old file_ops grab-bag.
//
// Schema:
//
//	{
//	  "src":  string (required) — path to copy (file or folder)
//	  "dest": string (required) — destination path or folder
//	}
//
// Behaviour (from fileops.Copy): if dest is an existing folder, src
// is copied INTO it; it NEVER overwrites an existing destination.
//
// Safety: BOTH src and dest are resolved with sandbox.ResolveSafe.
//
// Verification: "file_write" family.
type Copy struct {
	BaseDir string
}

// NewCopy returns a Copy tool rooted at baseDir.
func NewCopy(baseDir string) *Copy {
	return &Copy{BaseDir: baseDir}
}

type copyArgs struct {
	Src  string `json:"src"`
	Dest string `json:"dest"`
}

// Spec returns the tool definition.
func (t *Copy) Spec() Tool {
	return Tool{
		Name:        "copy",
		Description: "Copy a file or folder (folders are copied recursively). If dest is an existing folder, the item is copied into it. Never overwrites an existing destination.",
		Schema: `{
			"src":  {"type": "string", "description": "Path to copy (file or folder), relative to the project folder"},
			"dest": {"type": "string", "description": "Destination path, or an existing folder to copy into"}
		}`,
		Fn: t.execute,
	}
}

func (t *Copy) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a copyArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("copy: bad args: %w", err)}, nil
	}
	if a.Src == "" || a.Dest == "" {
		return Result{Err: fmt.Errorf("copy: src and dest are required")}, nil
	}
	srcFull, err := sandbox.ResolveSafe(t.BaseDir, a.Src)
	if err != nil {
		return Result{Err: fmt.Errorf("copy: src: %w", err)}, nil
	}
	dstFull, err := sandbox.ResolveSafe(t.BaseDir, a.Dest)
	if err != nil {
		return Result{Err: fmt.Errorf("copy: dest: %w", err)}, nil
	}
	if _, err := fileops.Copy(srcFull, dstFull); err != nil {
		return Result{Err: fmt.Errorf("copy: %w", err)}, nil
	}
	return Result{Text: fmt.Sprintf("Copied %s -> %s", a.Src, a.Dest)}, nil
}
