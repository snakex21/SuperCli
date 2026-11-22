package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

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
	ReviewNoChanges = "review: no file changes were recorded in this run"
	// ReviewNotChecked is neutral, not a "needs review": the workspace was
	// too large (or too slow) to fingerprint, so nothing is claimed either way.
	ReviewNotChecked    = "unchecked: file changes were not checked (workspace too large)"
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
		after := workspaceFingerprint(ctx, dir)
		if before == fingerprintUnchecked || after == fingerprintUnchecked || after == "" {
			return ReviewNotChecked
		}
		changed = after != before
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

// sameTask reports whether a brief retries a failed, interrupted or stopped
// worker's task, however worded: the same goal line ("CEL:/GOAL:/TASK:"), or
// briefs that share most of their meaningful words (project boilerplate
// excluded by using the goal lines when both have one). Identifiers (paths,
// file names, snake_case and CamelCase names) decide first: briefs from one
// template that name different identifiers are different tasks ("tests for
// internal/parser" vs "internal/lexer", "de.json" vs "fr.json",
// "TestLoginHandler" vs "TestLogoutHandler").
func sameTask(a, b string) bool {
	ga, gb := taskGoal(a), taskGoal(b)
	if ga != "" && gb != "" {
		a, b = ga, gb
	}
	ia, ib := taskIdentifiers(a), taskIdentifiers(b)
	if len(ia) > 0 && len(ib) > 0 {
		if len(ia) != len(ib) {
			return false
		}
		for id := range ia {
			if !ib[id] {
				return false
			}
		}
		// The same identifiers as a failed or stopped task: a retry of it
		// (only failed/interrupted/stopped workers are compared loosely).
		return true
	}
	wa, wb := taskWords(a), taskWords(b)
	if len(wa) == 0 || len(wb) == 0 {
		return false
	}
	shared := 0
	for w := range wa {
		if wb[w] {
			shared++
		}
	}
	smaller := len(wa)
	if len(wb) < smaller {
		smaller = len(wb)
	}
	union := len(wa) + len(wb) - shared
	// Most of the shorter brief is in the other, or the two overlap broadly.
	return float64(shared)/float64(smaller) >= 0.7 || float64(shared)/float64(union) >= 0.5
}

// taskIdentifiers returns the identifier-like words of a brief: paths, file
// names (a dot between letters), snake_case, CamelCase and letter+digit names.
func taskIdentifiers(s string) map[string]bool {
	out := map[string]bool{}
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(`,;:()[]{}"'`+"`", r)
	}) {
		tok = strings.Trim(tok, ".-")
		if len(tok) < 3 {
			continue
		}
		ident := strings.ContainsAny(tok, `/\_`)
		letters, digits := false, false
		prev := rune(0)
		for i, r := range tok {
			switch {
			case unicode.IsDigit(r):
				digits = true
			case unicode.IsLetter(r):
				letters = true
				if i > 0 && unicode.IsUpper(r) && unicode.IsLower(prev) {
					ident = true // CamelCase
				}
			case r == '.' && unicode.IsLetter(prev) && i+1 < len(tok):
				ident = true // file.ext, pkg.Name
			}
			prev = r
		}
		if ident || letters && digits {
			out[strings.ToLower(tok)] = true
		}
	}
	return out
}

func taskGoal(s string) string {
	for _, line := range strings.Split(s, "\n") {
		l := strings.TrimSpace(line)
		lower := strings.ToLower(l)
		for _, p := range []string{"cel:", "goal:", "task:", "zadanie:"} {
			if strings.HasPrefix(lower, p) {
				return strings.TrimSpace(l[len(p):])
			}
		}
	}
	return ""
}

// taskStopWords carry no task meaning ("please", "the", "i", "w").
var taskStopWords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true, "to": true, "of": true, "in": true, "on": true, "for": true, "with": true,
	"please": true, "again": true, "retry": true, "continue": true, "now": true, "it": true, "this": true, "that": true,
	"i": true, "w": true, "z": true, "na": true, "do": true, "oraz": true, "lub": true, "proszę": true, "prosze": true,
	"ponownie": true, "jeszcze": true, "raz": true, "dalej": true, "kontynuuj": true, "się": true, "sie": true,
}

func taskWords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) < 3 || taskStopWords[w] {
			continue
		}
		// Light stemming: inflections of one word count as one ("pająka", "pająk").
		if r := []rune(w); len(r) > 5 {
			w = string(r[:5])
		}
		out[w] = true
	}
	return out
}

// installReview attaches the review check to a worker this tool runs.
func (a *AgentTool) installReview(w *Worker) {
	if w != nil {
		w.reviewHook = a.reviewRun
		w.reviewSnapshot = a.reviewSnapshot
	}
}

// fingerprintUnchecked: the workspace could not be fingerprinted within the
// file and time bounds; no "changed"/"unchanged" verdict may rest on it.
const fingerprintUnchecked = "unchecked"

// workspaceFingerprint describes the state of dir: in a git work tree the
// HEAD commit, "git status --porcelain", the content of the unstaged and
// staged diffs and the size and modification time of every untracked file
// (so a second edit of an already modified file changes it too); elsewhere
// the count, total size and newest modification time of the files (build
// caches and VCS folders skipped). Two equal fingerprints mean nothing
// visible changed. fingerprintUnchecked when the walk hits its bounds, ""
// when it cannot be taken at all.
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
		// One status (untracked files listed one by one) and one diff against
		// HEAD (staged and unstaged content). Measured on a 46k-file repo:
		// ~0.26 s, against ~0.12 s for status alone; it runs in the worker's
		// goroutine at the start and end of a run, never on the GUI path.
		status, err := git("status", "--porcelain=v1", "-z", "--untracked-files=all")
		if err == nil {
			h := sha256.New()
			for _, part := range []string{head, status} {
				h.Write([]byte(part))
				h.Write([]byte{0})
			}
			diffs := [][]string{{"diff", "HEAD", "--no-ext-diff", "--binary"}}
			if strings.TrimSpace(head) == "" || strings.HasPrefix(strings.TrimSpace(head), "HEAD") {
				// No commit yet: compare the index and the work tree instead.
				diffs = [][]string{{"diff", "--no-ext-diff", "--binary"}, {"diff", "--cached", "--no-ext-diff", "--binary"}}
			}
			for _, args := range diffs {
				out, err := git(args...)
				if err != nil {
					return fingerprintUnchecked
				}
				h.Write([]byte(out))
				h.Write([]byte{0})
			}
			for _, entry := range strings.Split(status, "\x00") {
				name, ok := strings.CutPrefix(entry, "?? ")
				if !ok {
					continue
				}
				fmt.Fprintf(h, "%s", name)
				if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err == nil {
					fmt.Fprintf(h, ":%d:%d", info.Size(), info.ModTime().UnixNano())
				}
				h.Write([]byte{0})
			}
			if ctx.Err() != nil {
				return fingerprintUnchecked
			}
			return "git:" + hex.EncodeToString(h.Sum(nil))
		}
	}
	var files, size int64
	var newest time.Time
	var truncated bool
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			truncated = true
			return fs.SkipAll
		}
		if err != nil {
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
		if files++; files > fingerprintMaxFiles {
			truncated = true
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
	if truncated {
		return fingerprintUnchecked
	}
	return fmt.Sprintf("fs:%d:%d:%d", files, size, newest.UnixNano())
}

// fingerprintMaxFiles bounds the walk outside git (a variable for tests).
var fingerprintMaxFiles int64 = 50000
