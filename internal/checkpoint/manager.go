// Package checkpoint provides conflict-safe per-turn workspace undo without
// touching the user's Git index, branch, commits, or working-tree state.
package checkpoint

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"supercli/internal/system/childproc"
	"supercli/internal/tools"
)

var ErrUnavailable = errors.New("checkpoint unavailable (git executable is required)")

type Record struct {
	ID        string       `json:"id"`
	SessionID string       `json:"session_id"`
	UserSeq   int          `json:"user_seq,omitempty"`
	Prompt    string       `json:"prompt,omitempty"`
	Before    string       `json:"before"`
	After     string       `json:"after"`
	Files     []string     `json:"files"`
	Changes   []FileChange `json:"changes,omitempty"`
	Undone    bool         `json:"undone"`
	CreatedAt time.Time    `json:"created_at"`
}

// FileChange is the user-facing classification of one workspace change.
// Kinds are stable wire values consumed by the WebGUI: created, modified,
// deleted. Rename detection is disabled so moves are represented as one
// deletion and one creation, which is unambiguous and undo-safe.
type FileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type Result struct {
	Record    Record   `json:"record"`
	Files     []string `json:"files"`
	Conflicts []string `json:"conflicts,omitempty"`
}

// BatchResult is a newest-to-oldest set of checkpoints reverted together.
// Records is retained so a caller can roll the operation forward if a later
// step (for example creating the conversation branch) fails.
type BatchResult struct {
	Records   []Record `json:"records,omitempty"`
	Files     []string `json:"files,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
}

type Manager struct {
	mu               sync.Mutex
	home, repo, meta string
	excludes         string
	records          []Record
}

func Open(home, dataDir string) (*Manager, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, ErrUnavailable
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(abs))))
	root := filepath.Join(dataDir, "checkpoints", hex.EncodeToString(h[:8]))
	m := &Manager{home: abs, repo: filepath.Join(root, "objects.git"), meta: filepath.Join(root, "turns.json")}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	excludes := ".git/\n.supercli/\ncheckpoints/\nsessions.db*\n*.db-wal\n*.db-shm\n"
	if rel, relErr := filepath.Rel(abs, root); relErr == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		excludes += filepath.ToSlash(rel) + "/\n"
	}
	m.excludes = excludes
	if data, err := os.ReadFile(m.meta); err == nil {
		_ = json.Unmarshal(data, &m.records)
	}
	return m, nil
}

type Turn struct {
	mu                        sync.Mutex
	manager                   *Manager
	sessionID, prompt, before string
	userSeq                   int
	touched                   bool
}

// Controller reuses one manager across interactive TUI turns. Registered tool
// wrappers consult the currently active turn, so a new checkpoint can begin
// without rebuilding the tool registry or changing its prompt-visible schema.
type Controller struct {
	mu        sync.Mutex
	manager   *Manager
	sessionID string
	turns     []*Turn
}

func NewController(manager *Manager, sessionID string) *Controller {
	return &Controller{manager: manager, sessionID: sessionID}
}

func (c *Controller) Start(prompt string) {
	c.mu.Lock()
	c.turns = append(c.turns, c.manager.NewTurn(c.sessionID, prompt))
	c.mu.Unlock()
}

func (c *Controller) Wrap(spec tools.Tool) tools.Tool {
	if spec.ReadOnly || !mutatingTool(spec.Name) {
		return spec
	}
	original := spec.Fn
	spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		c.mu.Lock()
		var turn *Turn
		if len(c.turns) > 0 {
			turn = c.turns[len(c.turns)-1]
		}
		c.mu.Unlock()
		if turn != nil {
			if err := turn.ensureBefore(ctx); err != nil {
				return tools.Result{Err: fmt.Errorf("checkpoint before %s: %w", spec.Name, err)}, nil
			}
		}
		return original(ctx, args)
	}
	return spec
}

func (c *Controller) Complete(ctx context.Context) (*Record, error) {
	c.mu.Lock()
	var turn *Turn
	if len(c.turns) > 0 {
		turn = c.turns[0]
		c.turns = c.turns[1:]
	}
	c.mu.Unlock()
	if turn == nil {
		return nil, nil
	}
	return turn.Complete(ctx)
}
func (c *Controller) Undo(ctx context.Context) (Result, error) {
	r := c.manager.Latest(c.sessionID)
	if r == nil {
		return Result{}, os.ErrNotExist
	}
	return c.manager.Undo(ctx, r.ID)
}
func (c *Controller) Redo(ctx context.Context) (Result, error) {
	r := c.manager.Latest(c.sessionID)
	if r == nil {
		return Result{}, os.ErrNotExist
	}
	return c.manager.Redo(ctx, r.ID)
}

// Preview returns metadata for the next whole-turn undo/redo without touching
// the workspace. UI layers use it to show the exact file scope before asking
// for confirmation.
func (c *Controller) Preview(redo bool) (*Record, error) {
	r := c.manager.Latest(c.sessionID)
	if r == nil {
		return nil, os.ErrNotExist
	}
	if redo && !r.Undone {
		return nil, errors.New("nothing to redo")
	}
	if !redo && r.Undone {
		return nil, errors.New("nothing to undo")
	}
	return r, nil
}

func (m *Manager) NewTurn(sessionID, prompt string) *Turn {
	return &Turn{manager: m, sessionID: sessionID, prompt: clip(prompt, 160)}
}

// SetUserSeq associates the checkpoint with the user message that started the
// turn. It is optional for non-persistent callers, but enables precise GUI
// rewind of every file-changing turn at and after a selected message.
func (t *Turn) SetUserSeq(seq int) {
	t.mu.Lock()
	if seq > 0 {
		t.userSeq = seq
	}
	t.mu.Unlock()
}

// Wrap lazily captures the workspace immediately before the first mutating
// tool. Read-only/chat turns therefore pay zero checkpoint filesystem cost.
func (t *Turn) Wrap(spec tools.Tool) tools.Tool {
	if spec.ReadOnly || !mutatingTool(spec.Name) {
		return spec
	}
	original := spec.Fn
	spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		if err := t.ensureBefore(ctx); err != nil {
			return tools.Result{Err: fmt.Errorf("checkpoint before %s: %w", spec.Name, err)}, nil
		}
		return original(ctx, args)
	}
	return spec
}

func mutatingTool(name string) bool {
	switch name {
	case "edit_line", "edit_lines", "insert_after", "delete_lines", "write_file", "make_dir", "move", "copy", "trash", "ctx_execute", "edit_docx", "edit_xlsx":
		return true
	default:
		return false
	}
}

func (t *Turn) ensureBefore(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.touched {
		return nil
	}
	commit, err := t.manager.capture(ctx)
	if err != nil {
		return err
	}
	t.before, t.touched = commit, true
	return nil
}

func (t *Turn) Complete(ctx context.Context) (*Record, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.touched {
		return nil, nil
	}
	after, err := t.manager.capture(ctx)
	if err != nil {
		return nil, err
	}
	if after == t.before {
		return nil, nil
	}
	changes, err := t.manager.diffChanges(ctx, t.before, after)
	if err != nil {
		return nil, err
	}
	if len(changes) == 0 {
		return nil, nil
	}
	files := make([]string, 0, len(changes))
	for _, change := range changes {
		files = append(files, change.Path)
	}
	now := time.Now().UTC()
	sum := sha256.Sum256([]byte(t.sessionID + t.before + after + now.String()))
	rec := Record{ID: hex.EncodeToString(sum[:8]), SessionID: t.sessionID, UserSeq: t.userSeq, Prompt: t.prompt, Before: t.before, After: after, Files: files, Changes: changes, CreatedAt: now}
	if err := t.manager.append(rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (m *Manager) capture(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureRepoLocked(); err != nil {
		return "", err
	}
	if _, err := m.git(ctx, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	tree, err := m.git(ctx, "write-tree")
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", "--git-dir="+m.repo, "commit-tree", strings.TrimSpace(tree), "-m", "SuperCli checkpoint")
	childproc.HideWindow(cmd)
	cmd.Dir = m.home
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=SuperCli", "GIT_AUTHOR_EMAIL=checkpoint@local", "GIT_COMMITTER_NAME=SuperCli", "GIT_COMMITTER_EMAIL=checkpoint@local")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("checkpoint commit: %w: %s", err, out)
	}
	commit := strings.TrimSpace(string(out))
	_, _ = m.git(ctx, "update-ref", "refs/supercli/latest", commit)
	return commit, nil
}

func (m *Manager) ensureRepoLocked() error {
	if _, err := os.Stat(filepath.Join(m.repo, "HEAD")); err == nil {
		return nil
	}
	cmd := exec.Command("git", "init", "--bare", m.repo)
	childproc.HideWindow(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("checkpoint init: %w: %s", err, out)
	}
	return os.WriteFile(filepath.Join(m.repo, "info", "exclude"), []byte(m.excludes), 0o600)
}

func (m *Manager) git(ctx context.Context, args ...string) (string, error) {
	base := []string{"--git-dir=" + m.repo, "--work-tree=" + m.home}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	childproc.HideWindow(cmd)
	cmd.Dir = m.home
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
	out, err := m.git(ctx, "diff", "--name-status", "-z", "--no-renames", a, b)
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, r)
	return m.saveLocked()
}
func (m *Manager) saveLocked() error {
	data, err := json.MarshalIndent(m.records, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.meta + ".tmp"
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
func (m *Manager) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = nil
	return os.RemoveAll(filepath.Dir(m.repo))
}

// PreviewFrom reports non-undone checkpoints at or after a user message.
// Older records without UserSeq are deliberately excluded: guessing by prompt
// text could restore the wrong files when a prompt was repeated.
func (m *Manager) PreviewFrom(sessionID string, userSeq int) BatchResult {
	m.mu.Lock()
	defer m.mu.Unlock()
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
func (m *Manager) ForgetFrom(sessionID string, userSeq int) error {
	if strings.TrimSpace(sessionID) == "" || userSeq <= 0 {
		return errors.New("session id and positive user sequence are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := make([]Record, 0, len(m.records))
	changed := false
	for _, record := range m.records {
		if record.SessionID == sessionID && record.UserSeq >= userSeq && record.UserSeq > 0 {
			changed = true
			continue
		}
		kept = append(kept, record)
	}
	if !changed {
		return nil
	}
	previous := m.records
	m.records = kept
	if err := m.saveLocked(); err != nil {
		m.records = previous
		return err
	}
	return nil
}

// UndoFrom restores every recorded turn at and after userSeq, newest first.
// If any restore conflicts, already-applied restores are redone so callers do
// not observe a half-rewound workspace.
func (m *Manager) UndoFrom(ctx context.Context, sessionID string, userSeq int) (BatchResult, error) {
	preview := m.PreviewFrom(sessionID, userSeq)
	applied := BatchResult{Files: preview.Files}
	for _, record := range preview.Records {
		result, err := m.Undo(ctx, record.ID)
		if err != nil {
			applied.Conflicts = append(applied.Conflicts, result.Conflicts...)
			rollbackErr := m.RedoBatch(ctx, applied)
			if rollbackErr != nil {
				return applied, fmt.Errorf("%w; rollback failed: %v", err, rollbackErr)
			}
			return applied, err
		}
		applied.Records = append(applied.Records, result.Record)
	}
	return applied, nil
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
func (m *Manager) RedoIDs(ctx context.Context, sessionID string, ids []string) (BatchResult, error) {
	m.mu.Lock()
	batch := BatchResult{}
	files := map[string]struct{}{}
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			m.mu.Unlock()
			return BatchResult{}, errors.New("empty checkpoint id")
		}
		if _, duplicate := seen[id]; duplicate {
			m.mu.Unlock()
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
			m.mu.Unlock()
			return BatchResult{}, fmt.Errorf("checkpoint %q does not belong to session", id)
		}
		if !m.records[idx].Undone {
			m.mu.Unlock()
			return BatchResult{}, fmt.Errorf("checkpoint %q is not undone", id)
		}
		record := m.records[idx]
		batch.Records = append(batch.Records, record)
		for _, file := range record.Files {
			files[file] = struct{}{}
		}
	}
	m.mu.Unlock()
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
	return m.redoBatch(ctx, batch)
}

func (m *Manager) redoBatch(ctx context.Context, batch BatchResult) (BatchResult, error) {
	applied := make([]Record, 0, len(batch.Records))
	for i := len(batch.Records) - 1; i >= 0; i-- {
		result, err := m.Redo(ctx, batch.Records[i].ID)
		if err == nil {
			applied = append(applied, result.Record)
			continue
		}
		batch.Conflicts = append(batch.Conflicts, result.Conflicts...)
		var rollbackErr error
		for j := len(applied) - 1; j >= 0; j-- {
			if _, undoErr := m.Undo(ctx, applied[j].ID); undoErr != nil {
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
	m.mu.Lock()
	idx := -1
	for i := range m.records {
		if m.records[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.mu.Unlock()
		return Result{}, os.ErrNotExist
	}
	rec := m.records[idx]
	m.mu.Unlock()
	if redo && !rec.Undone {
		return Result{}, errors.New("checkpoint is not undone")
	}
	if !redo && rec.Undone {
		return Result{}, errors.New("checkpoint is already undone")
	}
	expect, target := rec.After, rec.Before
	if redo {
		expect, target = rec.Before, rec.After
	}
	conflicts := []string{}
	for _, p := range rec.Files {
		expected, _ := m.blobHash(ctx, expect, p)
		current, _ := m.currentHash(ctx, p)
		if expected != current {
			conflicts = append(conflicts, p)
		}
	}
	if len(conflicts) > 0 {
		return Result{Record: rec, Conflicts: conflicts}, fmt.Errorf("checkpoint conflicts in %d file(s)", len(conflicts))
	}
	for _, p := range rec.Files {
		data, mode, exists, err := m.blob(ctx, target, p)
		if err != nil {
			return Result{}, err
		}
		full := filepath.Join(m.home, filepath.FromSlash(p))
		if !within(m.home, full) {
			return Result{}, fmt.Errorf("unsafe checkpoint path %q", p)
		}
		if !exists {
			if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
				return Result{}, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return Result{}, err
		}
		perm := os.FileMode(0o644)
		if mode == "100755" {
			perm = 0o755
		}
		if mode == "120000" {
			return Result{}, fmt.Errorf("checkpoint symlink restore is not supported for %q", p)
		}
		if err := os.WriteFile(full, data, perm); err != nil {
			return Result{}, err
		}
		if err := os.Chmod(full, perm); err != nil {
			return Result{}, err
		}
	}
	m.mu.Lock()
	m.records[idx].Undone = !redo
	rec = m.records[idx]
	err := m.saveLocked()
	m.mu.Unlock()
	return Result{Record: rec, Files: rec.Files}, err
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
	out, err := m.git(ctx, "hash-object", "--", filepath.ToSlash(path))
	return strings.TrimSpace(out), err
}
func (m *Manager) blob(ctx context.Context, commit, path string) ([]byte, string, bool, error) {
	if h, _ := m.blobHash(ctx, commit, path); h == "" {
		return nil, "", false, nil
	}
	modeOut, err := m.git(ctx, "ls-tree", commit, "--", filepath.ToSlash(path))
	if err != nil {
		return nil, "", false, err
	}
	fields := strings.Fields(modeOut)
	mode := "100644"
	if len(fields) > 0 {
		mode = fields[0]
	}
	out, err := m.git(ctx, "show", commit+":"+filepath.ToSlash(path))
	return []byte(out), mode, true, err
}
func within(root, path string) bool {
	r, _ := filepath.Abs(root)
	p, _ := filepath.Abs(path)
	rel, err := filepath.Rel(r, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
