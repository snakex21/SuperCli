package files

import (
	"context"
	"encoding/json"
	"fmt"

	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

// PatchFile is the model-facing tool for exact text patches on an
// existing file. It is the only edit path offered to the model.
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
	// Old/New/ExpectedCount are the single-change shorthand. patch_file is
	// the only edit path now, so the commonest edit in the corpus — one
	// replacement in one line — must not cost the model a nested array. As
	// flat scalars they are also writable in the thin protocol's «key: value»
	// form, which arrays are not.
	Old           string `json:"old"`
	New           string `json:"new"`
	ExpectedCount int    `json:"expected_count"`
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
		Description: "The edit tool: exact text replacements in an existing file, atomic. " +
			"One change: old + new. Several: changes, in ONE call. " +
			"Indentation and line endings need not match the file exactly.",
		Schema: `{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Path relative to the project folder"},
				"old": {"type": "string", "description": "Text to replace"},
				"new": {"type": "string", "description": "Its replacement; empty deletes"},
				"expected_count": {"type": "integer", "description": "Matches to expect (default 1)", "minimum": 1},
				"base_hash": {"type": "string", "description": "SHA-256 of current contents; rejects stale edits"},
				"changes": {
					"type": "array",
					"description": "Several replacements instead of old/new",
					"items": {
						"type": "object",
						"properties": {
							"old": {"type": "string"},
							"new": {"type": "string"},
							"expected_count": {"type": "integer", "minimum": 1}
						},
						"required": ["old", "new"],
						"additionalProperties": false
					},
					"minItems": 1
				}
			},
			"required": ["path"],
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
	// The shorthand is one entry of the same list, so everything below —
	// atomicity, anchoring, diagnostics — is shared, and there is no second
	// code path to keep in step with the first.
	switch {
	case a.Old != "" && len(a.Changes) > 0:
		return Result{Err: fmt.Errorf("patch_file: old/new and changes are two ways to say the same thing; send one or the other")}, nil
	case a.Old != "":
		a.Changes = []patchFileChange{{Old: a.Old, New: a.New, ExpectedCount: a.ExpectedCount}}
	case len(a.Changes) == 0 && a.New != "":
		return Result{Err: fmt.Errorf("patch_file: new was given without old; add old, the exact text to replace")}, nil
	case len(a.Changes) == 0:
		return Result{Err: fmt.Errorf("patch_file: nothing to change; give old and new for one replacement, or changes for several")}, nil
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
	if res.Note != "" {
		text += " " + res.Note
	}
	return Result{Text: text, Inert: res.Duplicated}, nil
}
