package office

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxDocxBatchOperations = 100

// doBatch applies many native Word edits to a private working copy and swaps
// it into place only after every operation succeeds. Individual operations may
// rewrite that private ZIP, but the user's document is written once and gets a
// single backup. This removes model round-trips without weakening atomicity.
func (t *EditDocxTool) doBatch(full string, p editDocxArgs) (Result, error) {
	if len(p.Operations) == 0 {
		err := fmt.Errorf("edit_docx batch: operations must contain at least one edit")
		return Result{Err: err}, err
	}
	if len(p.Operations) > maxDocxBatchOperations {
		err := fmt.Errorf("edit_docx batch: too many operations: %d > %d", len(p.Operations), maxDocxBatchOperations)
		return Result{Err: err}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx batch: %w", err)}, err
	}
	if info.IsDir() || info.Size() > t.MaxDocxBytes {
		err := fmt.Errorf("edit_docx batch: invalid document size %d", info.Size())
		return Result{Err: err}, err
	}

	work, err := os.CreateTemp(filepath.Dir(full), "."+filepath.Base(full)+".batch-*")
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx batch: create working copy: %w", err)}, err
	}
	workPath := work.Name()
	defer os.Remove(workPath)
	defer os.Remove(workPath + ".bak")
	if err := copyIntoOpenFile(full, work); err != nil {
		work.Close()
		return Result{Err: fmt.Errorf("edit_docx batch: copy document: %w", err)}, err
	}
	if err := work.Close(); err != nil {
		return Result{Err: fmt.Errorf("edit_docx batch: close working copy: %w", err)}, err
	}

	summaries := make([]string, 0, len(p.Operations))
	for i, operation := range p.Operations {
		action := strings.TrimSpace(operation.Action)
		if action == "" || action == "batch" || action == "create" {
			err := fmt.Errorf("edit_docx batch: operation %d has unsupported action %q; nothing was written", i+1, operation.Action)
			return Result{Err: err}, err
		}
		opArgs := operation.args()
		opArgs.Action = action
		result, opErr := t.executeResolved(workPath, opArgs)
		if opErr != nil || result.Err != nil {
			cause := opErr
			if cause == nil {
				cause = result.Err
			}
			err := fmt.Errorf("edit_docx batch: operation %d (%s) failed: %w; original document was not changed", i+1, action, cause)
			return Result{Err: err}, err
		}
		summaries = append(summaries, fmt.Sprintf("%d:%s", i+1, action))
	}

	same, err := filesHaveSameSHA256(full, workPath)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx batch: verify working copy: %w", err)}, err
	}
	if same {
		return Result{Text: fmt.Sprintf("Batch completed but all %d operation(s) were no-ops. Original document was not changed and no backup was created.", len(p.Operations)), Inert: true}, nil
	}
	if p.DryRun {
		return Result{Text: fmt.Sprintf("Preview only: validated %d operation(s) as one atomic Word edit (%s). Original document was not changed.", len(p.Operations), strings.Join(summaries, ", "))}, nil
	}
	backup, err := backupAndReplace(full, workPath)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx batch: commit: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Applied %d Word operation(s) in one atomic batch (%s). Original backup: %s.", len(p.Operations), strings.Join(summaries, ", "), backup)}, nil
}

func copyIntoOpenFile(src string, dst *os.File) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if _, err := io.Copy(dst, in); err != nil {
		return err
	}
	return dst.Sync()
}

func fileSHA256(path string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

func filesHaveSameSHA256(a, b string) (bool, error) {
	ah, err := fileSHA256(a)
	if err != nil {
		return false, err
	}
	bh, err := fileSHA256(b)
	if err != nil {
		return false, err
	}
	return ah == bh, nil
}
