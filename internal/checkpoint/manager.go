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
	"strings"
	"sync"
	"time"

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
	case "edit_line", "edit_lines", "insert_after", "delete_lines", "write_file",
		"patch_file", "create_file",
		"make_dir", "move", "copy", "trash", "ctx_execute", "edit_docx", "edit_xlsx":
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
