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
	"sync/atomic"
	"time"

	tools "supercli/internal/tools/core"
)

var ErrUnavailable = errors.New("checkpoint unavailable (git executable is required)")

type Record struct {
	ID            string       `json:"id"`
	CompletionKey string       `json:"completion_key,omitempty"`
	SessionID     string       `json:"session_id"`
	UserSeq       int          `json:"user_seq,omitempty"`
	UserMessageID int64        `json:"user_message_id,omitempty"`
	Prompt        string       `json:"prompt,omitempty"`
	Before        string       `json:"before"`
	After         string       `json:"after"`
	Files         []string     `json:"files"`
	Changes       []FileChange `json:"changes,omitempty"`
	RawBytes      bool         `json:"raw_bytes,omitempty"`
	Undone        bool         `json:"undone"`
	CreatedAt     time.Time    `json:"created_at"`
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
	mu                   contextMutex
	home, repo, meta     string
	excludes             string
	excludedDataRel      string
	repoReady            bool
	records              []Record
	gate                 *StoreGate
	pendingMu            sync.Mutex
	pending              map[*Turn]struct{}
	usageCounter         *StoreUsageCounter
	usageTransaction     *checkpointUsageTransaction
	userReceiptValidator UserReceiptValidator
}

func Open(home, dataDir string) (*Manager, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, ErrUnavailable
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	dataAbs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(abs))))
	root := filepath.Join(dataAbs, "checkpoints", hex.EncodeToString(h[:8]))
	m := &Manager{home: abs, repo: filepath.Join(root, "objects.git"), meta: filepath.Join(root, "turns.json")}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	excludes := ".git/\n.supercli/\ncheckpoints/\nsessions.db*\n*.db-wal\n*.db-shm\n"
	if rel, relErr := filepath.Rel(abs, root); relErr == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		excludes += filepath.ToSlash(rel) + "/\n"
	}
	if rel, relErr := filepath.Rel(abs, dataAbs); relErr == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		m.excludedDataRel = filepath.ToSlash(rel)
		excludes += "/" + escapeExcludePath(m.excludedDataRel) + "/\n"
	}
	m.excludes = excludes
	m.gate, err = NewStoreGate(dataAbs)
	if err != nil {
		return nil, err
	}
	if err := m.reloadRecordsLocked(); err != nil {
		return nil, err
	}
	return m, nil
}

type Turn struct {
	mu                        contextMutex
	manager                   *Manager
	sessionID, prompt, before string
	userSeq                   int
	userMessageID             int64
	completionIdentity        atomic.Pointer[string]
	deferredRequested         atomic.Bool
	detachedUserSeq           bool
	touched                   bool
	beforePinned              bool
	wholeWorkspace            bool
	scopeRoots                []string
	active                    *activePins
	barrier                   TurnBarrier
	completed                 *Record
	snapshotAfter             string
	deferredOnce              sync.Once
	retentionOnce             sync.Once
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

func (c *Controller) SetSession(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessionID = id
}

func (c *Controller) currentSession() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID
}

func (c *Controller) Start(prompt string) {
	c.mu.Lock()
	c.turns = append(c.turns, c.manager.NewTurn(c.sessionID, prompt))
	c.mu.Unlock()
}

func (c *Controller) currentTurn() *Turn {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.turns) == 0 {
		return nil
	}
	return c.turns[len(c.turns)-1]
}

func (c *Controller) Wrap(spec tools.Tool) tools.Tool {
	original := spec.Fn
	if spec.Name == "task" || spec.Name == "send_message" {
		spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			if turn := c.currentTurn(); turn != nil {
				ctx = WithTurn(ctx, turn, &turn.barrier)
			}
			return original(ctx, args)
		}
		return spec
	}
	if spec.ReadOnly || !mutatingTool(spec.Name) {
		return spec
	}
	spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		ctx, cancel, err := checkpointCommandLifetime(ctx, spec.Name, args)
		if err != nil {
			return tools.Result{Err: err}, nil
		}
		defer cancel()
		if readOnlyCtx, readOnly := checkpointReadOnlyContext(ctx, spec.Name, args); readOnly {
			return original(readOnlyCtx, args)
		}
		ctx, err = c.manager.pinDownloadToolContext(ctx, spec.Name, args)
		if err != nil {
			return tools.Result{Err: fmt.Errorf("checkpoint before %s (tool did not run): %w", spec.Name, err)}, nil
		}
		fallback := c.currentTurn()
		var barrier *TurnBarrier
		if fallback != nil {
			barrier = &fallback.barrier
		}
		turn, leave, err := EnterBoundMutation(ctx, c.manager, fallback, barrier)
		if err != nil {
			return tools.Result{Err: fmt.Errorf("checkpoint before %s (tool did not run): %w", spec.Name, err)}, nil
		}
		defer leave()
		if err := turn.ensureBeforeForTool(ctx, spec.Name, args); err != nil {
			return tools.Result{Err: fmt.Errorf("checkpoint before %s (tool did not run): %w", spec.Name, err)}, nil
		}
		return original(ctx, args)
	}
	return spec
}

func (c *Controller) Complete(ctx context.Context) (*Record, error) {
	turn := c.takeOldestTurn()
	if turn == nil {
		return nil, nil
	}
	return turn.Complete(ctx)
}
func (c *Controller) Undo(ctx context.Context) (Result, error) {
	return c.manager.restoreLatest(ctx, c.currentSession(), false)
}
func (c *Controller) Redo(ctx context.Context) (Result, error) {
	return c.manager.restoreLatest(ctx, c.currentSession(), true)
}

// Preview returns metadata for the next whole-turn undo/redo without touching
// the workspace. UI layers use it to show the exact file scope before asking
// for confirmation.
func (c *Controller) Preview(redo bool) (*Record, error) {
	r := c.manager.Latest(c.currentSession())
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
// A removed chat tail permanently detaches its pending owner; a stale binder
// must not attach that checkpoint to a later message reusing the same sequence.
func (t *Turn) SetUserSeq(seq int) {
	t.mu.Lock()
	if seq > 0 && !t.detachedUserSeq && t.userMessageID == 0 {
		t.userSeq = seq
	}
	t.mu.Unlock()
}

// Wrap lazily captures the workspace immediately before the first mutating
// tool. Read-only/chat turns therefore pay zero checkpoint filesystem cost.
func (t *Turn) Wrap(spec tools.Tool) tools.Tool {
	original := spec.Fn
	if spec.Name == "task" || spec.Name == "send_message" {
		spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			return original(WithTurn(ctx, t, &t.barrier), args)
		}
		return spec
	}
	if spec.ReadOnly || !mutatingTool(spec.Name) {
		return spec
	}
	spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		ctx, cancel, err := checkpointCommandLifetime(ctx, spec.Name, args)
		if err != nil {
			return tools.Result{Err: err}, nil
		}
		defer cancel()
		if readOnlyCtx, readOnly := checkpointReadOnlyContext(ctx, spec.Name, args); readOnly {
			return original(readOnlyCtx, args)
		}
		ctx, err = t.manager.pinDownloadToolContext(ctx, spec.Name, args)
		if err != nil {
			return tools.Result{Err: fmt.Errorf("checkpoint before %s (tool did not run): %w", spec.Name, err)}, nil
		}
		turn, leave, err := EnterBoundMutation(ctx, t.manager, t, &t.barrier)
		if err != nil {
			return tools.Result{Err: fmt.Errorf("checkpoint before %s (tool did not run): %w", spec.Name, err)}, nil
		}
		defer leave()
		if err := turn.ensureBeforeForTool(ctx, spec.Name, args); err != nil {
			return tools.Result{Err: fmt.Errorf("checkpoint before %s (tool did not run): %w", spec.Name, err)}, nil
		}
		return original(ctx, args)
	}
	return spec
}

func mutatingTool(name string) bool {
	switch name {
	case "write_file", "patch_file", "create_file",
		"make_dir", "move", "copy", "trash", "ctx_execute", "edit_docx", "edit_xlsx", "web_download", "read_zip":
		return true
	default:
		return false
	}
}

func (t *Turn) ensureBefore(ctx context.Context) error {
	if err := t.mu.LockContext(ctx); err != nil {
		return err
	}
	defer t.mu.Unlock()
	return t.ensureWholeBeforeLocked(ctx)
}

func (t *Turn) ensureWholeBeforeLocked(ctx context.Context) error {
	if t.touched && t.wholeWorkspace {
		return t.ensureBeforePinLocked(ctx)
	}
	if err := t.ensureActivePinsLocked(); err != nil {
		return err
	}
	t.beforePinned = false
	commit, err := t.manager.captureSnapshotFilteredPinned(ctx, nil, t.before, t.scopeRoots, nil, false, t.active, "before")
	if err != nil {
		return err
	}
	t.before, t.touched = commit, true
	t.beforePinned = true
	t.wholeWorkspace = true
	return nil
}

// Seal reports whether completion can run immediately. UI callers can wait on
// ready once in the background when an accepted worker is still running.
func (t *Turn) Seal() (<-chan struct{}, bool) { return t.barrier.Seal() }

func (t *Turn) Complete(ctx context.Context) (*Record, error) {
	err := t.barrier.Complete(ctx, t.commitCheckpoint, t.releaseActivePins)
	if err == nil {
		if !t.mu.TryLock() {
			if lockErr := t.mu.LockContext(ctx); lockErr != nil {
				return nil, lockErr
			}
		}
		touched := t.touched
		t.mu.Unlock()
		if touched {
			// Collection failure leaves the shared ledger dirty; the next completed
			// mutating turn retries it. No timer or repeated scan for this owner.
			t.retentionOnce.Do(func() { err = t.manager.completeRetainedUsage(ctx, DefaultStoreBudgetBytes) })
		}
	}
	if !t.mu.TryLock() {
		if lockErr := t.mu.LockContext(ctx); lockErr != nil {
			return nil, errors.Join(err, lockErr)
		}
	}
	defer t.mu.Unlock()
	if t.completed == nil {
		return nil, err
	}
	record := *t.completed
	return &record, err
}

func (t *Turn) commitCheckpoint(ctx context.Context) (committed bool, err error) {
	if err := t.mu.LockContext(ctx); err != nil {
		return false, err
	}
	defer t.mu.Unlock()
	if !t.touched {
		return true, nil
	}
	if err := t.ensureBeforePinLocked(ctx); err != nil {
		return false, fmt.Errorf("checkpoint before finalization: %w", err)
	}
	var roots []string
	if !t.wholeWorkspace {
		roots = t.scopeRoots
	}
	var extras []string
	if t.wholeWorkspace {
		extras = t.scopeRoots // Explicitly edited ignored paths are still part of the turn.
	}
	after := t.snapshotAfter
	if after == "" {
		after, err = t.manager.captureSnapshotFilteredPinned(ctx, roots, "", nil, extras, false, t.active, "after")
		if after != "" {
			t.snapshotAfter = after
		}
		if err != nil {
			return false, fmt.Errorf("checkpoint after tool changes: %w", err)
		}
	}
	if after == t.before {
		return true, nil
	}
	changes, err := t.manager.diffChanges(ctx, t.before, after)
	if err != nil {
		return false, err
	}
	if len(changes) == 0 {
		return true, nil
	}
	files := make([]string, 0, len(changes))
	for _, change := range changes {
		files = append(files, change.Path)
	}
	now := time.Now().UTC()
	sum := sha256.Sum256([]byte(t.sessionID + t.before + after + now.String()))
	rec := Record{ID: hex.EncodeToString(sum[:8]), CompletionKey: t.CompletionKey(), SessionID: t.sessionID, UserSeq: t.userSeq, UserMessageID: t.userMessageID, Prompt: t.prompt, Before: t.before, After: after, Files: files, Changes: changes, RawBytes: true, CreatedAt: now}
	unlock, err := t.manager.lockStore(ctx)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	finishUsage, err := t.manager.beginUsageLocked(ctx)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, finishUsage(err == nil)) }()
	rec.Before, rec.After, err = t.recordSnapshotsLocked(ctx, t.before, after, files)
	if err != nil {
		return false, err
	}
	committed, err = t.manager.appendCommittedLocked(ctx, rec)
	if committed {
		t.completed = &rec
	}
	return committed, err
}
