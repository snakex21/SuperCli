package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

// Move is the thin tool that moves or renames a file or folder —
// one verb for both, like `mv` and `git mv`. It replaces the
// move/rename actions of the old file_ops grab-bag. Renaming is
// just moving to a new name, so there is deliberately no separate
// rename tool.
//
// Schema:
//
//	{
//	  "src":  string (required) — path to move (file or folder)
//	  "dest": string (required) — destination path or folder
//	}
//
// Behaviour (from fileops.Move): if dest is an existing folder, src
// moves INTO it; it NEVER overwrites an existing destination.
//
// Safety: BOTH src and dest are resolved with sandbox.ResolveSafe,
// so neither side can escape the project home.
//
// Verification: "file_write" family.
type Move struct {
	BaseDir string
}

// NewMove returns a Move tool rooted at baseDir.
func NewMove(baseDir string) *Move {
	return &Move{BaseDir: baseDir}
}

type moveArgs struct {
	Src  string `json:"src"`
	Dest string `json:"dest"`
}

// Spec returns the tool definition.
func (t *Move) Spec() Tool {
	return Tool{
		Name:        "move",
		Description: "Move or rename a file or folder (renaming = moving to a new name). If dest is an existing folder, the item moves into it. Never overwrites an existing destination.",
		Schema: `{
			"src":  {"type": "string", "description": "Path to move or rename (file or folder), relative to the project folder"},
			"dest": {"type": "string", "description": "Destination path, or an existing folder to move into"}
		}`,
		Fn: t.execute,
	}
}

func (t *Move) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a moveArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("move: bad args: %w", err)}, nil
	}
	if a.Src == "" || a.Dest == "" {
		return Result{Err: fmt.Errorf("move: src and dest are required")}, nil
	}
	srcFull, err := sandbox.ResolveSafe(t.BaseDir, a.Src)
	if err != nil {
		return Result{Err: fmt.Errorf("move: src: %w", err)}, nil
	}
	dstFull, err := sandbox.ResolveSafe(t.BaseDir, a.Dest)
	if err != nil {
		return Result{Err: fmt.Errorf("move: dest: %w", err)}, nil
	}
	if _, err := fileops.Move(srcFull, dstFull); err != nil {
		return Result{Err: fmt.Errorf("move: %w", err)}, nil
	}
	return Result{Text: fmt.Sprintf("Moved %s -> %s", a.Src, a.Dest)}, nil
}
