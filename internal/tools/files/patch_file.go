package files

import (
	"context"
	"encoding/json"
	"fmt"

	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

// PatchFile is the model-facing tool for exact text patches on an
// existing file. Prefer this over edit_line / write_file for mutations.
type PatchFile struct {
	BaseDir string
}

// NewPatchFile returns a PatchFile tool rooted at baseDir.
func NewPatchFile(baseDir string) *PatchFile {
	return &PatchFile{BaseDir: baseDir}
}

type patchFileArgs struct {
	Path     string            `json:"path"`
	BaseHash string            `json:"base_hash"`
	Changes  []patchFileChange `json:"changes"`
}

type patchFileChange struct {
	Old           string `json:"old"`
	New           string `json:"new"`
	ExpectedCount int    `json:"expected_count"`
}

// Spec returns the tool definition (full JSON Schema).
func (t *PatchFile) Spec() Tool {
	return Tool{
		Name: "patch_file",
		Description: "Apply one or more exact text replacements to an existing file (atomic: all-or-nothing). " +
			"Prefer this for edits. Do not use tool_search to find an editor — call patch_file directly. " +
			"Group multiple changes to the same file in one call. old must match exactly; expected_count defaults to 1.",
		Schema: `{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File path relative to the project folder"},
				"base_hash": {"type": "string", "description": "Optional SHA-256 of current file contents; rejects stale edits"},
				"changes": {
					"type": "array",
					"description": "Exact replacements applied in order",
					"items": {
						"type": "object",
						"properties": {
							"old": {"type": "string", "description": "Exact text to find (non-empty)"},
							"new": {"type": "string", "description": "Replacement text"},
							"expected_count": {"type": "integer", "description": "Exact number of non-overlapping matches (default 1)", "minimum": 1}
						},
						"required": ["old", "new"],
						"additionalProperties": false
					},
					"minItems": 1
				}
			},
			"required": ["path", "changes"],
			"additionalProperties": false
		}`,
		Fn: t.execute,
	}
}

func (t *PatchFile) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a patchFileArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("patch_file: bad args: %w", err)}, nil
	}
	if a.Path == "" {
		return Result{Err: fmt.Errorf("patch_file: path is required")}, nil
	}
	if len(a.Changes) == 0 {
		return Result{Err: fmt.Errorf("patch_file: changes is empty")}, nil
	}
	full, err := sandbox.ResolveSafe(t.BaseDir, a.Path)
	if err != nil {
		return Result{Err: fmt.Errorf("patch_file: %w", err)}, nil
	}
	chs := make([]fileops.PatchChange, len(a.Changes))
	for i, c := range a.Changes {
		chs[i] = fileops.PatchChange{Old: c.Old, New: c.New, ExpectedCount: c.ExpectedCount}
	}
	res, err := fileops.PatchFile(full, chs, a.BaseHash)
	if err != nil {
		return Result{Err: fmt.Errorf("patch_file: %w", err)}, nil
	}
	changed := "false"
	if res.Changed {
		changed = "true"
	}
	text := fmt.Sprintf(
		"Patched %s: replacements=%d changed=%s before_hash=%s after_hash=%s",
		a.Path, res.Replacements, changed, res.BeforeHash, res.AfterHash,
	)
	return Result{Text: text}, nil
}
