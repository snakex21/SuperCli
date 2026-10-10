// Package checkpoint provides conflict-safe per-turn workspace undo without
// touching the user's Git index, branch, commits, or working-tree state.
package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func (m *Manager) capture(ctx context.Context) (string, error) {
	return m.captureSnapshot(ctx, nil, "", nil)
}

func (m *Manager) ensureRepoLocked(ctx context.Context) error {
	if m.repoReady {
		return nil
	}
	existing := true
	if _, err := os.Stat(filepath.Join(m.repo, "HEAD")); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		existing = false
		if err := m.requireUsageCensusLocked(); err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, "git", "-c", "core.longpaths=true", "-c", "init.defaultRefFormat=files", "init", "--bare", "--object-format=sha1", m.repo)
		configureCheckpointCommand(cmd)
		cmd.Env = checkpointGitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("checkpoint init: %w: %s", err, out)
		}
	}
	excludePath := filepath.Join(m.repo, "info", "exclude")
	old, err := os.ReadFile(excludePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if string(old) != m.excludes {
		if err := m.requireUsageCensusLocked(); err != nil {
			return err
		}
		// Existing private indexes may already track data that was not previously
		// excluded. Ignore rules alone cannot remove tracked entries.
		if existing && m.excludedDataRel != "" {
			if _, err := m.git(ctx, "--literal-pathspecs", "rm", "-r", "--cached", "--force", "--ignore-unmatch", "--", m.excludedDataRel); err != nil {
				return err
			}
		}
		if err := os.WriteFile(excludePath, []byte(m.excludes), 0600); err != nil {
			return err
		}
	}
	if err := m.pinRecordsLocked(ctx, m.records); err != nil {
		return err
	}
	m.repoReady = true
	return nil
}

func (m *Manager) git(ctx context.Context, args ...string) (string, error) {
	if len(args) >= 2 && args[0] == "update-ref" {
		ref := args[1]
		if ref == "-d" && len(args) >= 3 {
			ref = args[2]
		}
		if err := m.prepareRefUpdatesLocked(ref); err != nil {
			return "", err
		}
	}
	cmd := m.gitCommand(ctx, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (m *Manager) diffFiles(ctx context.Context, a, b string) ([]string, error) {
	changes, err := m.diffChanges(ctx, a, b)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(changes))
	for _, change := range changes {
		files = append(files, change.Path)
	}
	return files, nil
}

func (m *Manager) diffChanges(ctx context.Context, a, b string) ([]FileChange, error) {
	out, err := m.git(ctx, "diff", "--no-ext-diff", "--no-textconv", "--name-status", "-z", "--no-renames", a, b)
	if err != nil {
		return nil, err
	}
	return parseNameStatus(out), nil
}

func parseNameStatus(out string) []FileChange {
	parts := strings.Split(out, "\x00")
	changes := make([]FileChange, 0, len(parts)/2)
	for i := 0; i < len(parts); {
		status := parts[i]
		i++
		if status == "" {
			continue
		}
		path := ""
		if tab := strings.IndexByte(status, '\t'); tab >= 0 {
			path = status[tab+1:]
			status = status[:tab]
		} else if i < len(parts) {
			path = parts[i]
			i++
		}
		if path == "" {
			continue
		}
		kind := "modified"
		switch status[0] {
		case 'A':
			kind = "created"
		case 'D':
			kind = "deleted"
		}
		changes = append(changes, FileChange{Path: filepath.ToSlash(path), Kind: kind})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

func (m *Manager) append(r Record) error {
	_, err := m.appendCommitted(context.Background(), r)
	return err
}

func (m *Manager) appendCommitted(ctx context.Context, r Record) (committed bool, err error) {
	unlock, err := m.lockStore(ctx)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	finishUsage, err := m.beginUsageLocked(ctx)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, finishUsage(err == nil)) }()
	return m.appendCommittedLocked(ctx, r)
}

// The caller holds StoreIO and fresh metadata, including while creating the
// minimal record trees. Pinning and metadata publish share that transaction.
func (m *Manager) appendCommittedLocked(ctx context.Context, r Record) (bool, error) {
	if err := m.requireNewRecordLocked(r); err != nil {
		return false, err
	}
	if err := m.pinNewRecordLocked(ctx, r); err != nil {
		return false, err
	}
	previous := m.records
	m.records = append(m.records, r)
	if err := m.saveLocked(); err != nil {
		m.records = previous
		// A failed admission owns only the refs it just created. Active roots
		// still protect the turn's original snapshots and recovery baseline.
		return false, errors.Join(err, m.rollbackNewRecordRefsLocked(r))
	}
	return true, nil
}
func (m *Manager) saveLocked() (err error) {
	if err := m.dirtyStandaloneUsageLocked(context.Background()); err != nil {
		return err
	}
	if _, err := checkpointMetadataStringsLowerBound(m.records); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.records, "", "  ")
	if err != nil {
		return err
	}
	if err := checkCheckpointMetadataBytes(int64(len(data))); err != nil {
		return err
	}
	if err := m.accountMetadataLocked(int64(len(data))); err != nil {
		return err
	}
	tmp := m.meta + ".tmp"
	defer os.Remove(tmp)
	if err = os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.meta)
}
func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

func (m *Manager) Latest(sessionID string) *Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.reloadRecordsLocked(); err != nil {
		return nil
	}
	return m.latestLocked(sessionID)
}

func (m *Manager) latestLocked(sessionID string) *Record {
	for i := len(m.records) - 1; i >= 0; i-- {
		if m.records[i].SessionID == sessionID {
			r := m.records[i]
			return &r
		}
	}
	return nil
}

// Clear drops all conversation-linked checkpoints for this workspace. It is
// used only when the user explicitly deletes every conversation.
func (m *Manager) Clear() (err error) {
	return m.ClearContext(context.Background())
}

// ClearContext is the cancellable form of Clear. It deletes only this
// Manager's private checkpoint subtree under the portable store gate.
func (m *Manager) ClearContext(ctx context.Context) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.RetryRecordedCompletions(ctx); err != nil {
		return err
	}

	unlock, err := m.lockStore(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if err := m.requireNoActiveTurnLocked(ctx); err != nil {
		return err
	}
	data := filepath.Dir(m.gate.path)
	root := filepath.Join(data, "checkpoints", workspaceCheckpointKey(m.home))
	actual := filepath.Dir(m.repo)
	if resolved, resolveErr := filepath.EvalSymlinks(actual); resolveErr == nil {
		actual = resolved
	}
	if !pathEqual(actual, root) {
		return ErrStoreInventory
	}
	if err := retentionSafePath(data, root); err != nil {
		return err
	}
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		m.records, m.repoReady = nil, false
		return ctx.Err()
	} else if err != nil {
		return err
	}
	plan := workspaceCleanupStore{root: root}
	entries := 0
	if err := walkWorkspaceCleanup(ctx, data, root, 0, &entries, &plan, true); err != nil {
		return err
	}
	if err := m.dirtyStandaloneUsageLocked(ctx); err != nil {
		return err
	}
	var removed WorkspaceCleanupPreview
	if err := removeWorkspaceCleanupStore(ctx, data, plan, &removed); err != nil {
		if removed.Files != 0 {
			m.records, m.repoReady = nil, false
		}
		return err
	}
	m.records = nil
	m.repoReady = false
	return nil
}

// PreviewFrom reports non-undone checkpoints at or after a user message.
// Older records without UserSeq are deliberately excluded: guessing by prompt
// text could restore the wrong files when a prompt was repeated.
func (m *Manager) PreviewFrom(sessionID string, userSeq int) BatchResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.reloadRecordsLocked(); err != nil {
		return BatchResult{}
	}
	return m.previewFromLocked(sessionID, userSeq)
}

func (m *Manager) previewFromLocked(sessionID string, userSeq int) BatchResult {
	result := BatchResult{}
	files := map[string]struct{}{}
	for i := len(m.records) - 1; i >= 0; i-- {
		r := m.records[i]
		if r.SessionID != sessionID || r.UserSeq < userSeq || r.UserSeq == 0 || r.Undone {
			continue
		}
		result.Records = append(result.Records, r)
		for _, file := range r.Files {
			files[file] = struct{}{}
		}
	}
	for file := range files {
		result.Files = append(result.Files, file)
	}
	sort.Strings(result.Files)
	return result
}

// ForgetFrom detaches checkpoints belonging to transcript turns that were
// permanently removed by an in-place conversation rewind. It never changes
// workspace files. This makes the files currently on disk the new baseline
// and prevents newly appended messages (which can reuse sequence numbers)
// from colliding with checkpoints from the discarded conversation tail.
func (m *Manager) ForgetFrom(sessionID string, userSeq int) (err error) {
	if strings.TrimSpace(sessionID) == "" || userSeq <= 0 {
		return errors.New("session id and positive user sequence are required")
	}
	// The caller has already truncated history. Detach late owners even if
	// metadata I/O fails; a later SetUserSeq must not link a reused message.
	// Never take Turn.mu from inside the Manager/store transaction.
	m.detachPendingUserSequences(sessionID, userSeq)
	unlock, err := m.lockStore(context.Background())
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return m.forgetFromLocked(context.Background(), sessionID, userSeq)
}

// UndoFrom restores every recorded turn at and after userSeq, newest first.
// If any restore conflicts, already-applied restores are redone so callers do
// not observe a half-rewound workspace.
func (m *Manager) UndoFrom(ctx context.Context, sessionID string, userSeq int) (out BatchResult, err error) {
	if err := m.RetryRecordedCompletions(ctx); err != nil {
		return BatchResult{}, err
	}

	unlock, err := m.lockStore(ctx)
	if err != nil {
		return BatchResult{}, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	// Pending workers can exist before their first record. Empty previews do
	// not make a file rewind safe, and expired history cannot be half-restored.
	if err := m.requireNoActiveTurnLocked(ctx); err != nil {
		return BatchResult{}, err
	}
	if err := m.requireRetentionHistoryFromLocked(sessionID, userSeq); err != nil {
		return BatchResult{}, err
	}
	preview, err := m.validatedPreviewFromLocked(ctx, sessionID, userSeq)
	if err != nil {
		return BatchResult{}, err
	}
	return m.undoPreviewLocked(ctx, preview)
}

// RedoBatch reverses UndoFrom. Records are redone oldest first so consecutive
// snapshots of the same file are applied in chronological order.
func (m *Manager) RedoBatch(ctx context.Context, batch BatchResult) error {
	_, err := m.redoBatch(ctx, batch)
	return err
}

// RedoIDs restores one exact UndoFrom batch. IDs must be supplied in the
// newest-to-oldest order returned by UndoFrom; keeping the receipt explicit
// prevents an older, unrelated undone checkpoint from being restored.
func (m *Manager) RedoIDs(ctx context.Context, sessionID string, ids []string) (out BatchResult, err error) {
	if err := m.RetryRecordedCompletions(ctx); err != nil {
		return BatchResult{}, err
	}

	unlock, err := m.lockStore(ctx)
	if err != nil {
		return BatchResult{}, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	batch := BatchResult{}
	files := map[string]struct{}{}
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return BatchResult{}, errors.New("empty checkpoint id")
		}
		if _, duplicate := seen[id]; duplicate {
			return BatchResult{}, fmt.Errorf("duplicate checkpoint %q", id)
		}
		seen[id] = struct{}{}
		idx := -1
		for i := range m.records {
			if m.records[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 || m.records[idx].SessionID != sessionID {
			return BatchResult{}, fmt.Errorf("checkpoint %q does not belong to session", id)
		}
		if !m.records[idx].Undone {
			return BatchResult{}, fmt.Errorf("checkpoint %q is not undone", id)
		}
		record := m.records[idx]
		batch.Records = append(batch.Records, record)
		for _, file := range record.Files {
			files[file] = struct{}{}
		}
	}
	if len(batch.Records) == 0 {
		return BatchResult{}, errors.New("no checkpoints to restore")
	}
	for file := range files {
		batch.Files = append(batch.Files, file)
	}
	sort.SliceStable(batch.Records, func(i, j int) bool {
		return batch.Records[i].CreatedAt.After(batch.Records[j].CreatedAt)
	})
	sort.Strings(batch.Files)
	return m.redoBatchLocked(ctx, batch)
}

func (m *Manager) redoBatch(ctx context.Context, batch BatchResult) (out BatchResult, err error) {
	if err := m.RetryRecordedCompletions(ctx); err != nil {
		return BatchResult{}, err
	}

	unlock, err := m.lockStore(ctx)
	if err != nil {
		return BatchResult{}, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return m.redoBatchLocked(ctx, batch)
}

func (m *Manager) redoBatchLocked(ctx context.Context, batch BatchResult) (BatchResult, error) {
	applied := make([]Record, 0, len(batch.Records))
	for i := len(batch.Records) - 1; i >= 0; i-- {
		result, err := m.restoreLockedWithRename(ctx, batch.Records[i].ID, true, os.Rename)
		if err == nil {
			applied = append(applied, result.Record)
			continue
		}
		batch.Conflicts = append(batch.Conflicts, result.Conflicts...)
		var rollbackErr error
		for j := len(applied) - 1; j >= 0; j-- {
			if _, undoErr := m.restoreLockedWithRename(context.WithoutCancel(ctx), applied[j].ID, false, os.Rename); undoErr != nil {
				rollbackErr = errors.Join(rollbackErr, undoErr)
			}
		}
		if rollbackErr != nil {
			return batch, errors.Join(err, fmt.Errorf("redo rollback failed: %w", rollbackErr))
		}
		return batch, err
	}
	return batch, nil
}

func (m *Manager) Undo(ctx context.Context, id string) (Result, error) {
	return m.restore(ctx, id, false)
}
func (m *Manager) Redo(ctx context.Context, id string) (Result, error) {
	return m.restore(ctx, id, true)
}

func (m *Manager) restore(ctx context.Context, id string, redo bool) (Result, error) {
	return m.restoreWithRename(ctx, id, redo, os.Rename)
}

func (m *Manager) blobHash(ctx context.Context, commit, path string) (string, error) {
	out, err := m.git(ctx, "rev-parse", commit+":"+filepath.ToSlash(path))
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(out), nil
}
func (m *Manager) currentHash(ctx context.Context, path string) (string, error) {
	full := filepath.Join(m.home, filepath.FromSlash(path))
	info, err := os.Stat(full)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return "", err
	}
	out, err := m.git(ctx, "hash-object", "--no-filters", "--", filepath.ToSlash(path))
	return strings.TrimSpace(out), err
}
func within(root, path string) bool {
	r, _ := filepath.Abs(root)
	p, _ := filepath.Abs(path)
	rel, err := filepath.Rel(r, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// Selecting the latest point and restoring it is one store transaction.
// A second process cannot insert a newer record between these two steps.
func (m *Manager) restoreLatest(ctx context.Context, sessionID string, redo bool) (out Result, err error) {
	if err := m.RetryRecordedCompletions(ctx); err != nil {
		return Result{}, err
	}

	unlock, err := m.lockStore(ctx)
	if err != nil {
		return Result{}, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	record := m.latestLocked(sessionID)
	if record == nil {
		return Result{}, os.ErrNotExist
	}
	return m.restoreLockedWithRename(ctx, record.ID, redo, os.Rename)
}
