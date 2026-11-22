package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/checkpoint"
	"supercli/internal/system/childproc"
)

// Worker review (1.0.39). A worker's "done" means its run ended, not that the
// work is right. Two cheap facts are checked when a file-changing worker says
// done, and its card then shows "needs review" instead of "done":
//
//   - it changed no file at all in this run (a "fixed it" with an empty diff);
//   - the project's verify_commands (config, e.g. "go build ./...") fail
//     after its changes - checked only when such commands are configured.
//
// Review lines are English, one per finding, prefixed "review:"; hosts
// localize them by these exact texts.

const (
	ReviewNoChanges     = "review: no file changes were recorded in this run"
	reviewVerifyFailedF = "review: verify command failed: %s (exit %d)"
)

// reviewDir is where a worker's changes land.
func (a *AgentTool) reviewDir(w *Worker) string {
	if w != nil && w.Loop != nil && w.Loop.baseDir != "" {
		return w.Loop.baseDir
	}
	return a.sandboxRoot()
}

// reviewSnapshot fingerprints the workspace before a run (see
// workspaceFingerprint); "" when the worker cannot edit at all.
func (a *AgentTool) reviewSnapshot(ctx context.Context, w *Worker) string {
	if w == nil || w.Loop == nil || !checkpoint.HasCheckpointMutators(w.Loop.registry) {
		return ""
	}
	return workspaceFingerprint(ctx, a.reviewDir(w))
}

// reviewRun is the worker's reviewHook. before is the workspace fingerprint
// taken when the run started.
func (a *AgentTool) reviewRun(ctx context.Context, w *Worker, before string) string {
	if w == nil || w.Loop == nil || !checkpoint.HasCheckpointMutators(w.Loop.registry) {
		return "" // read-only workers (explore, advisor) are not expected to edit
	}
	dir := a.reviewDir(w)
	// File tools count directly; changes made by commands (git commit, go
	// generate, sed) show in the workspace fingerprint (review 4).
	changed := w.Loop.runMutations.Load() > 0
	if !changed && before != "" {
		changed = workspaceFingerprint(ctx, dir) != before
	}
	if !changed {
		return ReviewNoChanges
	}
	if len(a.VerifyCommands) == 0 {
		return ""
	}
	cfg := &DraftVerifyConfig{VerifyCommands: a.VerifyCommands, runCommand: a.verifyRunner}
	res := cfg.runSieve(ctx, dir)
	if res.Green {
		return ""
	}
	out := strings.Join(strings.Fields(res.Output), " ")
	if r := []rune(out); len(r) > 300 {
		out = "…" + string(r[len(r)-300:])
	}
	review := fmt.Sprintf(reviewVerifyFailedF, res.Command, res.Exit)
	if out != "" {
		review += " — " + out
	}
	return review
}

// installReview attaches the review check to a worker this tool runs.
func (a *AgentTool) installReview(w *Worker) {
	if w != nil {
		w.reviewHook = a.reviewRun
		w.reviewSnapshot = a.reviewSnapshot
	}
}

// workspaceFingerprint describes the state of dir cheaply: in a git work tree
// the HEAD commit plus "git status --porcelain" (tracked and untracked
// changes); elsewhere the count, total size and newest modification time of
// the files (build caches and VCS folders skipped, bounded). Two equal
// fingerprints mean nothing visible changed. "" when it cannot be taken.
func workspaceFingerprint(ctx context.Context, dir string) string {
	if dir == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		childproc.HideWindow(cmd)
		out, err := cmd.Output()
		return string(out), err
	}
	if inside, err := git("rev-parse", "--is-inside-work-tree"); err == nil && strings.TrimSpace(inside) == "true" {
		head, _ := git("rev-parse", "HEAD")
		status, err := git("status", "--porcelain=v1", "-z", "--untracked-files=normal")
		if err == nil {
			sum := sha256.Sum256([]byte(head + "\x00" + status))
			return "git:" + hex.EncodeToString(sum[:])
		}
	}
	var files, size int64
	var newest time.Time
	const maxFiles = 50000
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return filepath.SkipDir
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".hg", ".svn", "node_modules", ".zig-cache", "zig-cache", "zig-out", "target", "__pycache__", ".venv", ".supercli",
				"supercli-data", "checkpoints", "logs", "session-media":
				if path != dir {
					return filepath.SkipDir
				}
			}
			return nil
		}
		// App state that changes on its own (databases, logs) is not work.
		switch ext := strings.ToLower(filepath.Ext(d.Name())); ext {
		case ".db", ".db-wal", ".db-shm", ".log", ".lock":
			return nil
		}
		if files++; files > maxFiles {
			return fs.SkipAll
		}
		if info, err := d.Info(); err == nil {
			size += info.Size()
			if info.ModTime().After(newest) {
				newest = info.ModTime()
			}
		}
		return nil
	})
	return fmt.Sprintf("fs:%d:%d:%d", files, size, newest.UnixNano())
}
