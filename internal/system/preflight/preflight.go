// Package preflight builds a compact repo-state block that is
// appended ONCE to the first user message of a session (config
// `preflight_repo`, default ON), so the model does not burn its
// first 2-5 turns rediscovering where it is: current branch/HEAD,
// pending changes, recent commits, recently modified files.
//
// Placement contract: the block belongs on the VARIABLE side of the
// prompt (a user message), never in the system prefix — injecting
// per-session volatile text at the front of the prompt would break
// the stable KV-cache prefix (see internal/llm/system_demote.go).
//
// Git is optional by design: the git BINARY is used via exec when
// present and the directory is a repo; otherwise a pure-Go fallback
// lists the most recently modified files from an ignore-aware tree
// walk. SuperCli never requires git.
package preflight

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/childproc"
	"supercli/internal/tools/search"
)

// DefaultBudget is the hard token cap of the block. Sections are
// added most-important-first and trimmed line by line, so the least
// important content (old commits, extra files) is cut first.
const DefaultBudget = 300

const (
	defaultMaxCommits = 8
	defaultMaxFiles   = 10
	// Listing every path is useful for a small worktree, but it becomes a
	// prompt tax in exactly the repositories where an agent is most useful.
	// Above this threshold we send counts, hot areas and a short sample.
	defaultMaxStatusFiles = 16
	defaultMaxStatusAreas = 6
	gitTimeout            = 5 * time.Second
	defaultMaxScanEntries = 2048
	fallbackTimeout       = 200 * time.Millisecond
	// Static repo identity changes far less often than working-tree status.
	// A short cache collapses repeated preflights from coordinator/workers
	// without hiding fresh file edits: status is deliberately never cached.
	gitStaticCacheTTL = 2 * time.Second
)

var gitStaticCache sync.Map

type gitStaticState struct {
	at   time.Time
	head string
	log  string
}

// Options configures Build. The zero value uses the real git binary
// (when present) and the default budget; tests inject LookPath /
// RunGit to simulate a machine without git or canned repo state.
type Options struct {
	// Budget is the hard token cap (llm.EstimateTokens). 0 = DefaultBudget.
	Budget int
	// LookPath resolves the git binary. nil = exec.LookPath.
	LookPath func(file string) (string, error)
	// RunGit runs `git -C root args...`. Status output must preserve its
	// leading porcelain columns; trailing line endings may be removed.
	// Calls may run concurrently. nil = the real subprocess (with a shared
	// deadline). Any error from a
	// git call just drops that section — never fails the build.
	RunGit func(root string, args ...string) (string, error)
	// Now anchors the "recently modified" fallback. Zero = time.Now.
	Now time.Time
}

// EstimateTokens prices a block the same way the loop prices prompt
// content, so telemetry and the budget agree.
func EstimateTokens(block string) int {
	if block == "" {
		return 0
	}
	return llm.EstimateTokens([]llm.Message{{Role: llm.RoleUser, Content: block}})
}

// Build assembles the repo-state block for root, hard-capped at the
// token budget. Returns "" when there is nothing useful to say
// (e.g. an empty directory and no git).
func Build(root string, o Options) string {
	return BuildContext(context.Background(), root, o)
}

// BuildContext lets an interrupted foreground turn cancel optional startup
// work. Real Git calls share one deadline instead of each spending five seconds.
func BuildContext(ctx context.Context, root string, o Options) string {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return ""
	}
	cacheStaticGit := o.LookPath == nil && o.RunGit == nil && o.Now.IsZero()
	budget := o.Budget
	if budget <= 0 {
		budget = DefaultBudget
	}
	lookPath := o.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	runGit := o.RunGit
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}

	// sections, most important first. Each is a header plus lines;
	// the assembler adds whole lines while the budget allows.
	type section struct {
		header string
		lines  []string
	}
	var secs []section

	gitOK := false
	if executable, err := lookPath("git"); err == nil {
		if runGit == nil {
			// Resolve once for this collection. Command("git") would search
			// PATH again for every read; later builds still resolve it afresh.
			runGit = func(root string, args ...string) (string, error) {
				return realRunGitExecutable(ctx, executable, root, args...)
			}
		}
		// Porcelain status also carries the current branch. Collect the log in
		// parallel instead of spawning a separate branch process first.
		var status string
		var statusErr error
		statusDone := make(chan struct{})
		go func() {
			defer close(statusDone)
			status, statusErr = runGit(root, "status", "--porcelain", "--branch")
		}()
		head, lg := loadGitLog(root, runGit, cacheStaticGit)
		<-statusDone
		branch, worktree, hasBranch := statusBranch(status)
		if hasBranch {
			status = worktree
		}
		if (statusErr != nil || !hasBranch) && !gitStatusNotRepository(statusErr) {
			// Injected/legacy runners may return porcelain without its header.
			// A failed status must not erase independent repository identity.
			if identity, err := runGit(root, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
				branch = identity
			}
		}
		if branch != "" {
			gitOK = true
			id := "branch: " + branch
			if head != "" {
				id += "\nHEAD: " + head
			}
			secs = append(secs, section{lines: strings.Split(id, "\n")})
			// Working-tree state is intentionally never cached. Agent edits must
			// be visible immediately even when several workers share the static
			// branch/commit snapshot above.
			if statusErr == nil {
				if status == "" {
					secs = append(secs, section{lines: []string{"working tree clean"}})
				} else {
					secs = append(secs, section{header: "uncommitted changes:", lines: compactStatus(status)})
				}
			}
			if lg != "" {
				if lines := recentCommitLines(head, lg); len(lines) > 0 {
					secs = append(secs, section{header: "recent commits:", lines: lines})
				}
			}
		}
	}
	if !gitOK {
		// This is a startup hint, not an exhaustive index. Large directories
		// must not delay the first answer just to find ten recent filenames.
		scanCtx, cancel := context.WithTimeout(ctx, fallbackTimeout)
		files, complete := recentFiles(scanCtx, root, defaultMaxFiles, now)
		cancel()
		if len(files) > 0 {
			header := "recently modified files:"
			if !complete {
				header = "recent files (partial scan):"
			}
			secs = append(secs, section{header: header, lines: files})
		}
	}
	if len(secs) == 0 {
		return ""
	}

	// Assemble under the hard budget: whole lines, priority order.
	out := "Repo state (auto-collected):"
	for _, s := range secs {
		block := out
		if s.header != "" {
			cand := block + "\n" + s.header
			if EstimateTokens(cand) > budget {
				break
			}
			block = cand
		}
		added := false
		for _, ln := range s.lines {
			cand := block + "\n" + ln
			if EstimateTokens(cand) > budget {
				break
			}
			block = cand
			added = true
		}
		if s.header != "" && !added {
			// Header without a single line is noise — drop it.
			continue
		}
		out = block
	}
	if out == "Repo state (auto-collected):" {
		return ""
	}
	return out
}

// Only the native Git subprocess's unambiguous non-repository diagnostic makes
// another identity lookup redundant. Timeouts, bare repositories, permissions,
// translated/unknown messages and injected runner errors retain the fallback.
func gitStatusNotRepository(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && strings.HasPrefix(strings.TrimSpace(string(exit.Stderr)), "fatal: not a git repository")
}

func loadGitLog(root string, runGit func(string, ...string) (string, error), cacheable bool) (head, lg string) {
	key := filepath.Clean(root)
	if cacheable {
		if cached, ok := gitStaticCache.Load(key); ok {
			state := cached.(gitStaticState)
			if time.Since(state.at) < gitStaticCacheTTL {
				return state.head, state.log
			}
		}
	}
	// One log call supplies both the HEAD display line and recent commits.
	// The previous implementation spawned a separate `git log -1` process.
	lg, _ = runGit(root, "log", "--oneline", "-"+itoa(defaultMaxCommits))
	if lines := splitLines(lg); len(lines) > 0 {
		head = lines[0]
	}
	if cacheable {
		if head != "" {
			gitStaticCache.Store(key, gitStaticState{at: time.Now(), head: head, log: lg})
		}
	}
	return head, lg
}

// The --branch header uses separators forbidden in ref names. Only the header
// is parsed; porcelain file columns and quoted rename/path payloads stay opaque.
func statusBranch(status string) (branch, worktree string, ok bool) {
	header, rest, _ := strings.Cut(status, "\n")
	header = strings.TrimSuffix(header, "\r")
	if !strings.HasPrefix(header, "## ") {
		return "", status, false
	}
	branch = strings.TrimPrefix(header, "## ")
	if branch == "HEAD (no branch)" {
		return "HEAD", rest, true
	}
	if strings.HasPrefix(branch, "No commits yet on ") || strings.HasPrefix(branch, "Initial commit on ") {
		return "", rest, true
	}
	branch, _, _ = strings.Cut(branch, "...")
	branch, _, _ = strings.Cut(branch, " [")
	if branch == "" || strings.ContainsAny(branch, " \t\r") {
		return "", status, false
	}
	return branch, rest, true
}

// realRunGit observes the shared build deadline and always waits for process
// completion. CommandContext also handles cancellation before Start safely.
func realRunGit(ctx context.Context, root string, args ...string) (string, error) {
	return realRunGitExecutable(ctx, "git", root, args...)
}

func realRunGitExecutable(ctx context.Context, executable, root string, args ...string) (string, error) {
	full := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, executable, full...)
	// Preserve the original command argv even though cmd.Path is resolved.
	cmd.Args[0] = "git"
	childproc.HideWindow(cmd)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if len(args) > 0 && args[0] == "status" {
		// Porcelain's leading two columns are data. TrimSpace would remove
		// the first column of a worktree-only modification and corrupt the
		// first path when compactStatus parses the fixed status/path boundary.
		return strings.TrimRight(string(out), "\r\n"), err
	}
	return strings.TrimSpace(string(out)), err
}

// The first log entry already appears as HEAD. Remove only that exact first
// duplicate, retaining every other commit and preserving the source order.
func recentCommitLines(head, log string) []string {
	lines := splitLines(log)
	if head != "" && len(lines) > 0 && lines[0] == head {
		return lines[1:]
	}
	return lines
}

// recentFiles retains only the newest n entries in a bounded workspace sample.
// DirEntry.Info reuses enumeration metadata on Windows instead of a second stat.
func recentFiles(ctx context.Context, root string, n int, now time.Time) ([]string, bool) {
	type entry struct {
		rel string
		mod time.Time
	}
	top := make([]entry, 0, n)
	complete, err := search.WalkFileEntriesBounded(ctx, root, defaultMaxScanEntries, func(path string, d fs.DirEntry) error {
		fi, err := d.Info()
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		e := entry{rel: filepath.ToSlash(rel), mod: fi.ModTime()}
		pos := sort.Search(len(top), func(i int) bool {
			return e.mod.After(top[i].mod) || (e.mod.Equal(top[i].mod) && e.rel < top[i].rel)
		})
		if pos >= n {
			return nil
		}
		if len(top) < n {
			top = append(top, entry{})
		}
		copy(top[pos+1:], top[pos:len(top)-1])
		top[pos] = e
		return nil
	})
	out := make([]string, 0, len(top))
	for _, e := range top {
		out = append(out, e.rel+" ("+age(now.Sub(e.mod))+")")
	}
	return out, complete && err == nil
}

// age renders a duration compactly: 45s, 12m, 3h, 5d.
func age(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return itoa(int(d.Hours())) + "h"
	default:
		return itoa(int(d.Hours()/24)) + "d"
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func splitLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if ln = strings.TrimRight(ln, "\r"); strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	return out
}

// compactStatus preserves the exact porcelain listing for a small worktree.
// Large dirty trees are summarized so the first coordinator turn does not pay
// hundreds of path tokens merely to learn that many files changed. Porcelain
// status is deliberately parsed only at the stable two-column status/path
// boundary; rename payloads and unusual filenames stay opaque display text.
func compactStatus(status string) []string {
	// Keep only the small exact listing. Once the threshold is crossed,
	// count the remaining lines directly instead of allocating two complete
	// slices of an arbitrarily large porcelain output.
	var lines, sample []string
	var types, areas map[string]int
	total := 0
	for line := range strings.SplitSeq(status, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		total++
		if total <= defaultMaxStatusFiles {
			if lines == nil {
				lines = make([]string, 0, defaultMaxStatusFiles)
			}
			lines = append(lines, line)
			continue
		}
		if total == defaultMaxStatusFiles+1 {
			types = make(map[string]int)
			areas = make(map[string]int)
			sample = make([]string, 0, defaultMaxStatusFiles)
			for _, first := range lines {
				code, path := porcelainParts(first)
				types[statusKind(code)]++
				areas[statusArea(path)]++
				sample = append(sample, path)
			}
		}
		code, path := porcelainParts(line)
		types[statusKind(code)]++
		areas[statusArea(path)]++
	}
	if total <= defaultMaxStatusFiles {
		return lines
	}

	type orderCount struct {
		name  string
		count int
	}
	orderedTypes := make([]orderCount, 0, len(types))
	for _, name := range []string{"modified", "untracked", "added", "deleted", "renamed", "conflicted", "other"} {
		if n := types[name]; n > 0 {
			orderedTypes = append(orderedTypes, orderCount{name, n})
		}
	}
	typeParts := make([]string, 0, len(orderedTypes))
	for _, item := range orderedTypes {
		typeParts = append(typeParts, item.name+" "+itoa(item.count))
	}

	orderedAreas := make([]orderCount, 0, len(areas))
	for name, count := range areas {
		orderedAreas = append(orderedAreas, orderCount{name, count})
	}
	sort.Slice(orderedAreas, func(i, j int) bool {
		if orderedAreas[i].count != orderedAreas[j].count {
			return orderedAreas[i].count > orderedAreas[j].count
		}
		return orderedAreas[i].name < orderedAreas[j].name
	})
	if len(orderedAreas) > defaultMaxStatusAreas {
		orderedAreas = orderedAreas[:defaultMaxStatusAreas]
	}
	areaParts := make([]string, 0, len(orderedAreas))
	for _, item := range orderedAreas {
		areaParts = append(areaParts, item.name+" "+itoa(item.count))
	}

	return []string{
		"total: " + itoa(total) + " (" + strings.Join(typeParts, ", ") + ")",
		"areas: " + strings.Join(areaParts, ", "),
		"sample: " + strings.Join(sample, ", ") + " (and " + itoa(total-len(sample)) + " more)",
	}
}

func porcelainParts(line string) (code, path string) {
	if len(line) >= 3 {
		return line[:2], strings.TrimSpace(line[3:])
	}
	return line, strings.TrimSpace(line)
}

func statusKind(code string) string {
	switch {
	case code == "??":
		return "untracked"
	case strings.Contains(code, "U") || code == "AA" || code == "DD":
		return "conflicted"
	case strings.Contains(code, "R"):
		return "renamed"
	case strings.Contains(code, "D"):
		return "deleted"
	case strings.Contains(code, "A"):
		return "added"
	case strings.Contains(code, "M"):
		return "modified"
	default:
		return "other"
	}
}

func statusArea(path string) string {
	path = strings.Trim(path, `"`)
	path = strings.ReplaceAll(path, `\`, "/")
	if arrow := strings.LastIndex(path, " -> "); arrow >= 0 {
		path = strings.TrimSpace(path[arrow+4:])
	}
	first, rest, nested := strings.Cut(path, "/")
	if nested && first == "internal" {
		second, _, _ := strings.Cut(rest, "/")
		return path[:len(first)+1+len(second)]
	}
	if nested {
		return first
	}
	return "root"
}
