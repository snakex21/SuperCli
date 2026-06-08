package darwin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// requireGit skips the test if git is not on PATH.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available on PATH: %v", err)
	}
}

// makeRepo creates a fresh git repo in a temp dir with one initial commit.
func makeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@x"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	for _, args := range [][]string{
		{"add", "a.txt"},
		{"commit", "-q", "-m", "initial"},
	} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

// branchExists checks if a git branch still exists locally.
func branchExists(t *testing.T, dir, branch string) bool {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", "rev-parse", "--verify", branch)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd.Run() == nil
}

func TestWorktreeManager_HasGit_TrueForRealRepo(t *testing.T) {
	requireGit(t)
	dir := makeRepo(t)
	m := NewWorktreeManager(dir)
	if !m.HasGit() {
		t.Fatal("expected HasGit() == true for a real git repo")
	}
}

func TestWorktreeManager_HasGit_FalseForNonGitDir(t *testing.T) {
	requireGit(t)
	dir := t.TempDir() // empty, no .git
	m := NewWorktreeManager(dir)
	if m.HasGit() {
		t.Fatal("expected HasGit() == false for a non-git dir")
	}
}

func TestWorktreeManager_HasGit_FalseForNilOrEmpty(t *testing.T) {
	var nilMgr *WorktreeManager
	if nilMgr.HasGit() {
		t.Fatal("nil manager should return false from HasGit()")
	}
	empty := NewWorktreeManager("")
	if empty.HasGit() {
		t.Fatal("empty base should return false from HasGit()")
	}
	if got := empty.Base(); got != "" {
		t.Errorf("Base() = %q, want empty string", got)
	}
}

func TestWorktreeManager_Create(t *testing.T) {
	requireGit(t)
	dir := makeRepo(t)
	m := NewWorktreeManager(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	branch, path, err := m.Create(ctx)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if branch == "" {
		t.Error("empty branch name returned")
	}
	if path == "" {
		t.Error("empty worktree path returned")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("path should be absolute, got %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("worktree path not accessible: %v", err)
	}
	// The branch should start with "darwin-".
	if !startsWith(branch, "darwin-") {
		t.Errorf("branch name %q should start with 'darwin-'", branch)
	}
}

func TestWorktreeManager_CreateAndCleanupLeavesBaseClean(t *testing.T) {
	requireGit(t)
	dir := makeRepo(t)
	m := NewWorktreeManager(dir)
	ctx := context.Background()
	branch, path, err := m.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Cleanup(ctx, branch); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	// The worktree directory should be gone.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("worktree path should be removed, stat err = %v", err)
	}
	// The branch should be gone.
	if branchExists(t, dir, branch) {
		t.Errorf("branch %q should be deleted", branch)
	}
}

func TestWorktreeManager_CleanupIsIdempotent(t *testing.T) {
	requireGit(t)
	dir := makeRepo(t)
	m := NewWorktreeManager(dir)
	ctx := context.Background()
	branch, _, err := m.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Cleanup(ctx, branch); err != nil {
		t.Fatalf("first Cleanup: %v", err)
	}
	// Second call on the same branch should be a no-op (not an error).
	if err := m.Cleanup(ctx, branch); err != nil {
		t.Fatalf("second Cleanup returned error: %v", err)
	}
	// Unknown branch should also be a no-op.
	if err := m.Cleanup(ctx, "definitely-not-a-real-branch"); err != nil {
		t.Errorf("Cleanup of unknown branch returned error: %v", err)
	}
}

func TestWorktreeManager_CleanupAllCleansMultipleWorktrees(t *testing.T) {
	requireGit(t)
	dir := makeRepo(t)
	m := NewWorktreeManager(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	branches := make([]string, 3)
	for i := range branches {
		b, _, err := m.Create(ctx)
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		branches[i] = b
	}
	m.CleanupAll(ctx)
	for _, b := range branches {
		if branchExists(t, dir, b) {
			t.Errorf("branch %q should be removed by CleanupAll", b)
		}
	}
}

func TestWorktreeManager_MergeIntoProtectedBranchRefused(t *testing.T) {
	requireGit(t)
	dir := makeRepo(t)
	m := NewWorktreeManager(dir)
	ctx := context.Background()
	branch, _, err := m.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Cleanup(ctx, branch) })

	for _, target := range []string{"main", "master", "develop", "MAIN", "Master"} {
		if err := m.Merge(ctx, branch, target); err == nil {
			t.Errorf("expected error merging into protected branch %q, got nil", target)
		}
	}
}

func TestWorktreeManager_MergeIntoNonProtectedBranch(t *testing.T) {
	requireGit(t)
	dir := makeRepo(t)
	m := NewWorktreeManager(dir)
	ctx := context.Background()
	branch, _, err := m.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Cleanup(ctx, branch) })

	// The worktree was branched off HEAD, so the merge is a no-op
	// (or a merge commit with both parents the same). Either way,
	// git returns 0.
	if err := m.Merge(ctx, branch, "feature-x"); err != nil {
		t.Errorf("Merge into non-protected branch failed: %v", err)
	}
}

func TestWorktreeManager_MergeEmptyBranch(t *testing.T) {
	requireGit(t)
	dir := makeRepo(t)
	m := NewWorktreeManager(dir)
	if err := m.Merge(context.Background(), "", "feature"); err == nil {
		t.Error("expected error merging an empty branch name")
	}
}

func TestWorktreeManager_EmptyBaseManager(t *testing.T) {
	m := NewWorktreeManager("")
	if m.HasGit() {
		t.Error("empty base: HasGit() should be false")
	}
	if _, _, err := m.Create(context.Background()); err == nil {
		t.Error("Create on empty base should return error")
	}
}

func TestWorktreeManager_IsProtected(t *testing.T) {
	cases := []struct {
		branch string
		want   bool
	}{
		{"main", true},
		{"master", true},
		{"develop", true},
		{"MAIN", true},   // case-insensitive
		{"Master", true}, // case-insensitive
		{"feature", false},
		{"feature-x", false},
		{"", false},
		{"main-fork", false}, // only exact match
	}
	for _, c := range cases {
		if got := isProtected(c.branch); got != c.want {
			t.Errorf("isProtected(%q) = %v, want %v", c.branch, got, c.want)
		}
	}
}

// startsWith is a tiny helper that avoids the strings import overhead.
func startsWith(s, prefix string) bool {
	if len(prefix) > len(s) {
		return false
	}
	return s[:len(prefix)] == prefix
}
