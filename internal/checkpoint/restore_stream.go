package checkpoint

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"supercli/internal/tools/sandbox"
)

type restoreBlob struct {
	hash   string
	mode   os.FileMode
	size   int64
	exists bool
}

type restorePlan struct {
	path             string
	expected, target restoreBlob
	temporary        string
}

// The rename argument permits portable fault-injection tests of rollback. All
// production callers use os.Rename. The manager lock also prevents ForgetFrom
// from changing a record's index while a large restore is being streamed.
func (m *Manager) restoreWithRename(ctx context.Context, id string, redo bool, rename func(string, string) error) (out Result, err error) {
	if err := m.RetryRecordedCompletions(ctx); err != nil {
		return Result{}, err
	}

	unlock, err := m.lockStore(ctx)
	if err != nil {
		return Result{}, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return m.restoreLockedWithRename(ctx, id, redo, rename)
}

func (m *Manager) restoreLockedWithRename(ctx context.Context, id string, redo bool, rename func(string, string) error) (Result, error) {
	idx := -1
	for i := range m.records {
		if m.records[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Result{}, os.ErrNotExist
	}
	record := m.records[idx]
	if err := m.requireNoActiveTurnLocked(ctx); err != nil {
		return Result{Record: record}, err
	}
	if redo && !record.Undone {
		return Result{}, errors.New("checkpoint is not undone")
	}
	if !redo && record.Undone {
		return Result{}, errors.New("checkpoint is already undone")
	}
	expect, target := record.After, record.Before
	if redo {
		expect, target = record.Before, record.After
	}
	plans := make([]restorePlan, 0, len(record.Files))
	conflicts := []string{}
	for _, path := range record.Files {
		if _, err := m.safeRestorePath(path); err != nil {
			return Result{Record: record, Conflicts: []string{path}}, err
		}
		expected, err := m.inspectRestoreBlob(ctx, expect, path)
		if err != nil {
			return Result{Record: record}, err
		}
		current, err := m.currentHash(ctx, path)
		if err != nil {
			return Result{Record: record, Conflicts: []string{path}}, fmt.Errorf("checkpoint compare %q: %w", path, err)
		}
		if !record.RawBytes {
			if err := m.legacyByteLoss(ctx, path, expected.hash, current); err != nil {
				return Result{Record: record, Conflicts: []string{path}}, err
			}
		}
		if expected.hash != current {
			conflicts = append(conflicts, path)
		}
		blob, err := m.inspectRestoreBlob(ctx, target, path)
		if err != nil {
			return Result{Record: record}, err
		}
		plans = append(plans, restorePlan{path: path, expected: expected, target: blob})
	}
	if len(conflicts) > 0 {
		return Result{Record: record, Conflicts: conflicts}, fmt.Errorf("checkpoint conflicts in %d file(s)", len(conflicts))
	}
	// Validate all conflicts, legacy conversion and modes before creating even
	// a temporary file. Stage all blobs before changing any workspace file.
	defer func() {
		for _, plan := range plans {
			m.removeRestoreTemporary(plan.temporary)
		}
	}()
	for i := range plans {
		if !plans[i].target.exists {
			continue
		}
		temporary, err := m.stageRestoreBlob(ctx, plans[i].path, plans[i].target)
		if err != nil {
			return Result{Record: record}, err
		}
		plans[i].temporary = temporary
	}
	// Streaming a historical multi-gigabyte blob can take time. A manual edit
	// during staging still wins, and no staged file is promoted on conflict.
	for _, plan := range plans {
		if err := m.checkRestoreState(ctx, plan.path, plan.expected); err != nil {
			return Result{Record: record, Conflicts: []string{plan.path}}, err
		}
	}
	applied := make([]restorePlan, 0, len(plans))
	fail := func(err error, conflict string) (Result, error) {
		rollbackErr := m.rollbackRestore(context.WithoutCancel(ctx), applied, rename)
		result := Result{Record: record}
		if conflict != "" {
			result.Conflicts = []string{conflict}
		}
		if rollbackErr != nil {
			return result, errors.Join(err, fmt.Errorf("checkpoint rollback failed: %w", rollbackErr))
		}
		return result, err
	}
	for _, plan := range plans {
		if err := m.checkRestoreState(ctx, plan.path, plan.expected); err != nil {
			return fail(err, plan.path)
		}
		if err := m.promoteRestore(plan.path, plan.target, plan.temporary, rename); err != nil {
			return fail(err, "")
		}
		applied = append(applied, plan)
	}
	m.records[idx].Undone = !redo
	if err := m.saveLocked(); err != nil {
		m.records[idx].Undone = record.Undone
		return fail(err, "")
	}
	return Result{Record: m.records[idx], Files: record.Files}, nil
}

func (m *Manager) inspectRestoreBlob(ctx context.Context, commit, path string) (restoreBlob, error) {
	out, err := m.git(ctx, "--literal-pathspecs", "ls-tree", "--full-tree", "-l", "-z", commit, "--", filepath.ToSlash(path))
	if err != nil {
		return restoreBlob{}, err
	}
	if out == "" {
		return restoreBlob{}, nil
	}
	entry := strings.TrimSuffix(out, "\x00")
	tab := strings.IndexByte(entry, '\t')
	if tab < 0 || strings.Contains(entry, "\x00") {
		return restoreBlob{}, fmt.Errorf("invalid checkpoint entry for %q", path)
	}
	fields := strings.Fields(entry[:tab])
	if len(fields) != 4 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
		return restoreBlob{}, fmt.Errorf("checkpoint cannot safely restore non-regular path %q", path)
	}
	if len(fields[2]) != 40 {
		return restoreBlob{}, fmt.Errorf("invalid checkpoint blob hash for %q", path)
	}
	if _, err := hex.DecodeString(fields[2]); err != nil {
		return restoreBlob{}, fmt.Errorf("invalid checkpoint blob hash for %q", path)
	}
	size, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil || size < 0 {
		return restoreBlob{}, fmt.Errorf("invalid checkpoint size for %q", path)
	}
	mode := os.FileMode(0644)
	if fields[0] == "100755" {
		mode = 0755
	}
	return restoreBlob{hash: fields[2], mode: mode, size: size, exists: true}, nil
}

func (m *Manager) safeRestorePath(path string) (string, error) {
	full := filepath.Join(m.home, filepath.FromSlash(path))
	if !within(m.home, full) || pathEqual(full, m.home) {
		return "", fmt.Errorf("unsafe checkpoint path %q", path)
	}
	if _, err := sandbox.ResolveWithin(m.home, path); err != nil {
		return "", fmt.Errorf("unsafe checkpoint path %q: %w", path, err)
	}
	// A later symlink/junction anywhere below the workspace root must not
	// redirect staging, promotion, rollback or temporary-file cleanup.
	for check := full; !pathEqual(check, m.home); check = filepath.Dir(check) {
		info, err := os.Lstat(check)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("checkpoint path %q contains a later symlink or junction", path)
		}
		if check == full && !info.Mode().IsRegular() {
			return "", fmt.Errorf("checkpoint path %q is no longer a regular file", path)
		}
		if check != full && !info.IsDir() {
			return "", fmt.Errorf("checkpoint path %q has a non-directory ancestor", path)
		}
		if filepath.Dir(check) == check {
			return "", fmt.Errorf("unsafe checkpoint ancestor for %q", path)
		}
	}
	return full, nil
}

func (m *Manager) checkRestoreState(ctx context.Context, path string, expected restoreBlob) error {
	if _, err := m.safeRestorePath(path); err != nil {
		return err
	}
	current, err := m.currentHash(ctx, path)
	if err != nil {
		return err
	}
	if current != expected.hash {
		return fmt.Errorf("checkpoint conflict in %q", path)
	}
	return nil
}

func (m *Manager) stageRestoreBlob(ctx context.Context, path string, blob restoreBlob) (string, error) {
	full, err := m.safeRestorePath(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		return "", err
	}
	if _, err := m.safeRestorePath(path); err != nil {
		return "", err
	}
	output, err := os.CreateTemp(filepath.Dir(full), ".supercli-restore-")
	if err != nil {
		return "", err
	}
	temporary := output.Name()
	keep := false
	defer func() {
		output.Close()
		if !keep {
			m.removeRestoreTemporary(temporary)
		}
	}()
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := m.gitCommand(childCtx, "cat-file", "blob", blob.hash)
	stderr := &boundedRestoreError{}
	cmd.Stderr = stderr
	stream, err := startCheckpointCommandStream(childCtx, cmd)
	if err != nil {
		return "", err
	}
	stdout := stream.Stdout
	hash := sha1.New()
	_, _ = fmt.Fprintf(hash, "blob %d%c", blob.size, 0)
	// Hiding io.ReaderFrom keeps the copy buffer fixed even for os.File.
	n, copyErr := io.CopyBuffer(writerOnly{io.MultiWriter(output, hash)}, &contextReader{ctx: ctx, reader: io.LimitReader(stdout, blob.size+1)}, make([]byte, 32<<10))
	if copyErr != nil || n != blob.size {
		cancel()
	}
	waitErr := stream.Wait()
	if copyErr != nil {
		return "", fmt.Errorf("checkpoint stream %q: %w", path, errors.Join(ctx.Err(), copyErr, waitErr))
	}
	if waitErr != nil {
		return "", fmt.Errorf("checkpoint stream %q: %w: %s", path, waitErr, stderr.String())
	}
	if n != blob.size {
		return "", fmt.Errorf("checkpoint blob size changed for %q", path)
	}
	if hex.EncodeToString(hash.Sum(nil)) != blob.hash {
		return "", fmt.Errorf("checkpoint blob content is corrupt for %q", path)
	}
	if err := output.Chmod(blob.mode); err != nil {
		return "", err
	}
	if err := output.Sync(); err != nil {
		return "", err
	}
	if err := output.Close(); err != nil {
		return "", err
	}
	keep = true
	return temporary, nil
}

func (m *Manager) promoteRestore(path string, target restoreBlob, temporary string, rename func(string, string) error) error {
	full, err := m.safeRestorePath(path)
	if err != nil {
		return err
	}
	if !target.exists {
		if err := os.Remove(full); err != nil {
			return fmt.Errorf("checkpoint remove %q: %w", path, err)
		}
		return nil
	}
	if _, err := m.safeRestoreTemporary(temporary); err != nil {
		return err
	}
	if err := rename(temporary, full); err != nil {
		return fmt.Errorf("checkpoint replace %q: %w", path, err)
	}
	return nil
}

func (m *Manager) safeRestoreTemporary(temporary string) (string, error) {
	rel, err := filepath.Rel(m.home, temporary)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(filepath.Base(rel), ".supercli-restore-") {
		return "", errors.New("invalid checkpoint temporary path")
	}
	return m.safeRestorePath(filepath.ToSlash(rel))
}

func (m *Manager) removeRestoreTemporary(temporary string) {
	if temporary == "" {
		return
	}
	if full, err := m.safeRestoreTemporary(temporary); err == nil {
		_ = os.Remove(full)
	}
}

func (m *Manager) rollbackRestore(ctx context.Context, applied []restorePlan, rename func(string, string) error) error {
	var result error
	for i := len(applied) - 1; i >= 0; i-- {
		plan := applied[i]
		if err := m.checkRestoreState(ctx, plan.path, plan.target); err != nil {
			result = errors.Join(result, err)
			continue // Preserve a manual edit made after the attempted restore.
		}
		temporary := ""
		if plan.expected.exists {
			var err error
			temporary, err = m.stageRestoreBlob(ctx, plan.path, plan.expected)
			if err != nil {
				result = errors.Join(result, err)
				continue
			}
		}
		if err := m.checkRestoreState(ctx, plan.path, plan.target); err != nil {
			result = errors.Join(result, err)
		} else if err := m.promoteRestore(plan.path, plan.expected, temporary, rename); err != nil {
			result = errors.Join(result, err)
		}
		m.removeRestoreTemporary(temporary)
	}
	return result
}

type writerOnly struct{ io.Writer }

type boundedRestoreError struct{ bytes.Buffer }

func (w *boundedRestoreError) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 4096 - w.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = w.Buffer.Write(p)
	}
	return n, nil
}
