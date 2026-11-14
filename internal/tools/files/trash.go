package files

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	core "supercli/internal/tools/core"
	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

// Trash is the thin tool for removing a file or folder. There is
// deliberately NO hard-delete: the item is moved into the
// project's .supercli/trash folder with a timestamp, so removal is
// always recoverable. This replaces the trash action of the old
// file_ops grab-bag and is the only delete the model is given.
//
// Schema:
//
//	{
//	  "path": string (required) — file or folder to remove
//	}
//
// Safety: path is resolved with sandbox.ResolveSafe; the trash
// folder lives under home, so nothing leaves the project.
//
// Verification: "file_write" family.
type Trash struct {
	BaseDir string
	// Now is injected for deterministic timestamps in tests;
	// defaults to time.Now when nil.
	Now func() time.Time
}

// NewTrash returns a Trash tool rooted at baseDir.
func NewTrash(baseDir string) *Trash {
	return &Trash{BaseDir: baseDir, Now: time.Now}
}

type trashArgs struct {
	Path string `json:"path"`
}

// Spec returns the tool definition.
func (t *Trash) Spec() Tool {
	return Tool{
		Name:        "trash",
		Description: "Remove a file or folder by moving it to the project's trash folder (recoverable, never a hard delete). Use instead of deleting.",
		Schema: `{
			"path": {"type": "string", "description": "File or folder to remove, relative to the project folder"}
		}`,
		Fn:       t.execute,
		Approval: t.approval,
	}
}

// approval asks the user before trashing anything outside the workspace. That
// is only reachable in allow-all mode; workspace edits stay ungated.
func (t *Trash) approval(_ context.Context, args json.RawMessage) (*core.ApprovalRequest, error) {
	var a trashArgs
	if json.Unmarshal(args, &a) != nil || a.Path == "" {
		return nil, nil
	}
	if _, err := sandbox.ResolveWithin(t.BaseDir, a.Path); err == nil {
		return nil, nil
	}
	full, err := sandbox.ResolveSafe(t.BaseDir, a.Path)
	if err != nil {
		return nil, nil // execute reports the sandbox refusal
	}
	return &core.ApprovalRequest{
		Tool: "trash", Operation: "trash_outside_workspace", Risk: core.RiskWriteReversible, Reversible: true,
		Target:      full,
		Summary:     fmt.Sprintf("Remove %s, which is OUTSIDE the workspace? It is moved to the project's .supercli/trash folder, where it can be restored.", full),
		SummaryKey:  "approval.summary.trash_outside_workspace",
		SummaryArgs: map[string]string{"path": full},
		Scope:       []string{"trash", "trash_outside_workspace", "filesystem", filepath.Dir(full)},
		Count:       -1,
	}, nil
}

func (t *Trash) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a trashArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("trash: bad args: %w", err)}, nil
	}
	if a.Path == "" {
		return Result{Err: fmt.Errorf("trash: path is required")}, nil
	}
	full, err := sandbox.ResolveSafe(t.BaseDir, a.Path)
	if err != nil {
		return Result{Err: fmt.Errorf("trash: %w", err)}, nil
	}
	now := time.Now
	if t.Now != nil {
		now = t.Now
	}
	trashDir := filepath.Join(t.BaseDir, ".supercli", "trash")
	dst, err := fileops.TrashContext(ctx, full, trashDir, now())
	if err != nil {
		return Result{Err: fmt.Errorf("trash: %w", err)}, nil
	}
	return Result{Text: fmt.Sprintf("Moved %s to trash (%s). Restore by moving it back.", a.Path, dst)}, nil
}
