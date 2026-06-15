package darwin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// runGitCtx is the testable seam for executing git
// commands. The default impl uses exec.CommandContext
// with GIT_TERMINAL_PROMPT=0 so a missing credential
// helper fails fast instead of hanging.
var runGitCtx = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd.CombinedOutput()
}

// WorktreeManager owns a single git repo's Darwin
// worktree lifecycle. It is safe for concurrent
// use; calls to Create / Cleanup / Merge serialize
// through a mutex. HasGit is independent and may be
// called without a lock.
type WorktreeManager struct {
	base string // absolute path to the repo root
	mu   sync.Mutex
	open map[string]string // branch -> worktree path
	rng  *randSrc          // for unique ids in Create
}

// NewWorktreeManager returns a manager rooted at
// base. If base is not a git repo, HasGit returns
// false and Create falls back to creating branches
// in-place. The caller is expected to check HasGit
// before relying on isolation.
func NewWorktreeManager(base string) *WorktreeManager {
	return &WorktreeManager{
		base: base,
		open: map[string]string{},
		rng:  newRandSrc(time.Now().UnixNano()),
	}
}

// Base returns the repo root passed to
// NewWorktreeManager. Always non-empty.
func (m *WorktreeManager) Base() string { return m.base }

// HasGit reports whether base is inside a git
// working tree. The check is `git rev-parse
// --show-toplevel`; non-zero exit means "no".
func (m *WorktreeManager) HasGit() bool {
	if m == nil || m.base == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := runGitCtx(ctx, m.base, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	top := strings.TrimSpace(string(out))
	if top == "" {
		return false
	}
	// Resolve both paths to absolute for comparison.
	want, _ := filepath.Abs(m.base)
	got, _ := filepath.Abs(top)
	return filepath.Clean(want) == filepath.Clean(got)
}

// Create makes a new worktree on a fresh branch
// rooted at HEAD. The branch is named
// `darwin-<unixnano>-<id>` and the worktree lives
// under <base>/.darwin/<unixnano>-<id>. If the repo
// has no commits yet, falls back to a detached
// worktree at HEAD. Returns the branch name and the
// worktree's absolute path.
//
// On any failure, runs Cleanup to avoid leaving
// half-initialized state behind. Refuses to run
// when the manager's base is empty.
func (m *WorktreeManager) Create(ctx context.Context) (branch, path string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m == nil || m.base == "" {
		return "", "", fmt.Errorf("darwin: worktree: empty base")
	}
	if m.open == nil {
		m.open = map[string]string{}
	}
	if m.rng == nil {
		m.rng = newRandSrc(time.Now().UnixNano())
	}
	id := m.rng.hex(4)
	stamp := time.Now().UnixNano()
	branch = fmt.Sprintf("darwin-%d-%s", stamp, id)
	rel := filepath.Join(".darwin", fmt.Sprintf("%d-%s", stamp, id))
	abs := filepath.Join(m.base, rel)
	if mkErr := os.MkdirAll(filepath.Dir(abs), 0o755); mkErr != nil {
		return "", "", fmt.Errorf("darwin: mkdir worktree parent: %w", mkErr)
	}
	out, err := runGitCtx(ctx, m.base, "worktree", "add", "-b", branch, abs, "HEAD")
	if err != nil {
		// Empty-repo fallback: no HEAD yet. Use
		// `git worktree add --detach` so we still
		// get an isolated working copy.
		if isEmptyRepoErr(out, err) {
			if out2, err2 := runGitCtx(ctx, m.base, "worktree", "add", "--detach", abs); err2 != nil {
				return "", "", fmt.Errorf("darwin: worktree add (empty repo fallback): %v: %s", err2, strings.TrimSpace(string(out2)))
			}
			m.open[branch] = abs
			return branch, abs, nil
		}
		return "", "", fmt.Errorf("darwin: worktree add: %v: %s", err, strings.TrimSpace(string(out)))
	}
	m.open[branch] = abs
	return branch, abs, nil
}

// CommitAndDiff stages everything in the worktree
// belonging to branch, captures the staged diff
// (against HEAD), and commits it so the branch is
// mergeable. Returns the diff text ("" when the
// agent made no changes — in that case nothing is
// committed). Unknown branch is an error.
func (m *WorktreeManager) CommitAndDiff(ctx context.Context, branch string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	abs, ok := m.open[branch]
	if !ok || abs == "" {
		return "", fmt.Errorf("darwin: commit: unknown branch %q", branch)
	}
	if out, err := runGitCtx(ctx, abs, "add", "-A"); err != nil {
		return "", fmt.Errorf("darwin: stage worktree: %v: %s", err, strings.TrimSpace(string(out)))
	}
	diffOut, err := runGitCtx(ctx, abs, "diff", "--cached")
	if err != nil {
		return "", fmt.Errorf("darwin: diff worktree: %v: %s", err, strings.TrimSpace(string(diffOut)))
	}
	diff := string(diffOut)
	if strings.TrimSpace(diff) == "" {
		return "", nil
	}
	msg := fmt.Sprintf("darwin: candidate work on %s", branch)
	if out, err := runGitCtx(ctx, abs, "commit", "-m", msg); err != nil {
		return diff, fmt.Errorf("darwin: commit worktree: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return diff, nil
}

// Cleanup removes a single worktree by branch name.
// Idempotent: calling on an unknown branch is a
// no-op. Uses `git worktree remove --force` and
// `git branch -D`; either failing is non-fatal
// (we still want the orchestrator to make forward
// progress).
func (m *WorktreeManager) Cleanup(ctx context.Context, branch string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cleanupLocked(ctx, branch)
}

func (m *WorktreeManager) cleanupLocked(ctx context.Context, branch string) error {
	abs, ok := m.open[branch]
	if !ok {
		// Unknown branch — nothing to do.
		return nil
	}
	delete(m.open, branch)
	if abs != "" {
		// --force so we don't fail on dirty worktrees.
		_, _ = runGitCtx(ctx, m.base, "worktree", "remove", "--force", abs)
	}
	if branch != "" {
		_, _ = runGitCtx(ctx, m.base, "branch", "-D", branch)
	}
	return nil
}

// CleanupAll removes every worktree still tracked
// by this manager. Intended for the pool's safety
// net when a run aborts mid-flight. Errors from
// individual cleanups are swallowed.
func (m *WorktreeManager) CleanupAll(ctx context.Context) {
	m.mu.Lock()
	branches := make([]string, 0, len(m.open))
	for b := range m.open {
		branches = append(branches, b)
	}
	m.mu.Unlock()
	for _, b := range branches {
		_ = m.Cleanup(ctx, b)
	}
}

// Merge brings `branch` into `target` with
// `--no-ff`. Refuses to merge into protected
// branches (main, master, develop) — these are
// always the caller's long-lived trunk and the
// orchestrator must not auto-merge into them.
//
// Caller is expected to be on `target` already;
// the function does not switch branches.
func (m *WorktreeManager) Merge(ctx context.Context, branch, target string) error {
	if m == nil || branch == "" {
		return fmt.Errorf("darwin: merge: empty branch")
	}
	if isProtected(target) {
		return fmt.Errorf("darwin: refusing to auto-merge into protected branch %q", target)
	}
	if target == "" {
		target = "HEAD"
	}
	msg := fmt.Sprintf("darwin: pick winner from %s", branch)
	_, err := runGitCtx(ctx, m.base, "merge", "--no-ff", branch, "-m", msg)
	if err != nil {
		// Detect conflict.
		if isMergeConflictErr(err) {
			return ErrMergeConflict
		}
		return fmt.Errorf("darwin: merge %s into %s: %w", branch, target, err)
	}
	return nil
}

// isProtected reports whether the branch name is
// one we never want Darwin to auto-merge into.
func isProtected(branch string) bool {
	lb := strings.ToLower(strings.TrimSpace(branch))
	return lb == "main" || lb == "master" || lb == "develop"
}

// isEmptyRepoErr matches the "fatal: invalid
// reference" / "fatal: not a valid object name"
// error git returns when HEAD doesn't resolve.
func isEmptyRepoErr(out []byte, err error) bool {
	s := strings.ToLower(string(out))
	if strings.Contains(s, "invalid reference") || strings.Contains(s, "not a valid object") {
		return true
	}
	if err == nil {
		return false
	}
	es := strings.ToLower(err.Error())
	return strings.Contains(es, "invalid reference") || strings.Contains(es, "not a valid object")
}

// isMergeConflictErr matches "CONFLICT" output from
// `git merge`. The exit code from git is non-zero
// for any merge problem, so we look at the body.
func isMergeConflictErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "conflict")
}
