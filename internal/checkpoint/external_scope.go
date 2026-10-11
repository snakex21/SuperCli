package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tools "supercli/internal/tools/core"
	"supercli/internal/tools/sandbox"
)

// Full filesystem permission authorizes named file tools outside home, not an
// unbounded filesystem snapshot. Each external parent gets an ordinary scoped
// checkpoint manager, using the same portable data root and retention budget.
// The original turn barrier still covers the capture and actual mutation.
func (t *Turn) ensureExternalBeforeLocked(ctx context.Context, paths []string) error {
	// Resolution and policy validation were already done by toolScopeDetailed.
	// Ordinary workspace tools pay no duplicate filesystem reads here.
	if len(paths) == 0 {
		return nil
	}
	if !sandbox.IsUnsandboxed() {
		return sandbox.ErrEscape
	}
	for _, path := range paths {
		parent := filepath.Dir(path)
		key := parent
		if filepath.Separator == '\\' {
			key = strings.ToLower(key)
		}
		owner := t.external[key]
		if owner == nil {
			if t.manager.dataDir == "" {
				return errors.New("checkpoint: external path requires a configured portable data root")
			}
			manager, err := Open(parent, t.manager.dataDir)
			if err != nil {
				return fmt.Errorf("external checkpoint: %w", err)
			}
			owner = manager.NewTurn(t.sessionID, "External scoped changes: "+t.prompt)
			if t.external == nil {
				t.external = make(map[string]*Turn)
			}
			t.external[key] = owner
		}
		if owner.manager.snapshotExcluded(filepath.ToSlash(filepath.Base(path))) {
			return fmt.Errorf("external checkpoint: %q is protected application/checkpoint metadata", path)
		}
		// Capturing with a single-path tool avoids recursion through the other
		// operand of a cross-workspace copy/move; the actual Fn runs only once.
		args, _ := json.Marshal(map[string]string{"path": path})
		if err := owner.ensureBeforeForTool(ctx, "patch_file", args); err != nil {
			return fmt.Errorf("external checkpoint before %q: %w", path, err)
		}
	}
	return nil
}

func (t *Turn) completeExternalLocked(ctx context.Context) error {
	keys := make([]string, 0, len(t.external))
	for key := range t.external {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := t.external[key].Complete(ctx); err != nil {
			return fmt.Errorf("external checkpoint completion: %w", err)
		}
	}
	return nil
}

const ExternalCheckpointNotice = "[External file changes have a separate scoped checkpoint in application data; active-workspace undo covers only that workspace.]"

func (t *Turn) describeExternalCapture(name string, result *tools.Result) {
	switch name {
	case "write_file", "patch_file", "create_file", "make_dir", "trash", "edit_docx", "edit_xlsx", "read_zip", "copy", "move":
	default:
		return
	}
	if result.Err != nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.external) == 0 || t.externalNoticeSent {
		return
	}
	t.externalNoticeSent = true
	note := "\n" + ExternalCheckpointNotice
	result.Text += note
	if result.ModelPreview != "" {
		result.ModelPreview += note
	}
}
