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

	"supercli/internal/tools"
)

var ErrUnavailable = errors.New("checkpoint unavailable (git executable is required)")

type Record struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Prompt    string    `json:"prompt,omitempty"`
	Before    string    `json:"before"`
	After     string    `json:"after"`
	Files     []string  `json:"files"`
	Undone    bool      `json:"undone"`
	CreatedAt time.Time `json:"created_at"`
}

type Result struct {
	Record    Record   `json:"record"`
	Files     []string `json:"files"`
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

func (m *Manager) NewTurn(sessionID, prompt string) *Turn {
	return &Turn{manager: m, sessionID: sessionID, prompt: clip(prompt, 160)}
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
	files, err := t.manager.diffFiles(ctx, t.before, after)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	sum := sha256.Sum256([]byte(t.sessionID + t.before + after + now.String()))
	rec := Record{ID: hex.EncodeToString(sum[:8]), SessionID: t.sessionID, Prompt: t.prompt, Before: t.before, After: after, Files: files, CreatedAt: now}
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
	if out, err := exec.Command("git", "init", "--bare", m.repo).CombinedOutput(); err != nil {
		return fmt.Errorf("checkpoint init: %w: %s", err, out)
	}
	return os.WriteFile(filepath.Join(m.repo, "info", "exclude"), []byte(m.excludes), 0o600)
}

func (m *Manager) git(ctx context.Context, args ...string) (string, error) {
	base := []string{"--git-dir=" + m.repo, "--work-tree=" + m.home}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Dir = m.home
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (m *Manager) diffFiles(ctx context.Context, a, b string) ([]string, error) {
	out, err := m.git(ctx, "diff", "--name-only", "-z", a, b)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(out, "\x00")
	files := parts[:0]
	for _, p := range parts {
		if p != "" {
			files = append(files, filepath.ToSlash(p))
		}
	}
	sort.Strings(files)
	return files, nil
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
