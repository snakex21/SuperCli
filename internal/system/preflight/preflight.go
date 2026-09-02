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
	// Static repo identity changes far less often than working-tree status.
	// A short cache collapses repeated preflights from coordinator/workers
	// without hiding fresh file edits: status is deliberately never cached.
	gitStaticCacheTTL = 2 * time.Second
)

var gitStaticCache sync.Map

type gitStaticState struct {
	at     time.Time
	branch string
	head   string
	log    string
}

// Options configures Build. The zero value uses the real git binary
// (when present) and the default budget; tests inject LookPath /
// RunGit to simulate a machine without git or canned repo state.
type Options struct {
	// Budget is the hard token cap (llm.EstimateTokens). 0 = DefaultBudget.
	Budget int
	// LookPath resolves the git binary. nil = exec.LookPath.
	LookPath func(file string) (string, error)
	// RunGit runs `git -C root args...` and returns trimmed stdout.
	// nil = the real subprocess (with a timeout). Any error from a
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
	if runGit == nil {
		runGit = realRunGit
	}
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
	if _, err := lookPath("git"); err == nil {
		branch, head, lg := loadGitStatic(root, runGit, cacheStaticGit)
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
			if status, err := runGit(root, "status", "--porcelain"); err == nil {
				if status == "" {
					secs = append(secs, section{lines: []string{"working tree clean"}})
				} else {
					secs = append(secs, section{header: "uncommitted changes:", lines: compactStatus(status)})
				}
			}
			if lg != "" {
				secs = append(secs, section{header: "recent commits:", lines: splitLines(lg)})
			}
		}
	}
	if !gitOK {
		// Pure-Go fallback: most recently modified files by mtime.
		if files := recentFiles(root, defaultMaxFiles, now); len(files) > 0 {
			secs = append(secs, section{header: "recently modified files:", lines: files})
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

func loadGitStatic(root string, runGit func(string, ...string) (string, error), cacheable bool) (branch, head, lg string) {
	key := filepath.Clean(root)
	if cacheable {
		if cached, ok := gitStaticCache.Load(key); ok {
			state := cached.(gitStaticState)
			if time.Since(state.at) < gitStaticCacheTTL {
				return state.branch, state.head, state.log
			}
		}
	}
	branch, err := runGit(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || branch == "" {
		return "", "", ""
	}
	// One log call supplies both the HEAD display line and recent commits.
	// The previous implementation spawned a separate `git log -1` process.
	lg, _ = runGit(root, "log", "--oneline", "-"+itoa(defaultMaxCommits))
	if lines := splitLines(lg); len(lines) > 0 {
		head = lines[0]
	}
	if cacheable {
		gitStaticCache.Store(key, gitStaticState{at: time.Now(), branch: branch, head: head, log: lg})
	}
	return branch, head, lg
}

// realRunGit executes `git -C root args...` with a timeout so a hung
// git (e.g. a dead network filesystem) cannot stall session start.
func realRunGit(root string, args ...string) (string, error) {
	full := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", full...)
	childproc.HideWindow(cmd)
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.Output()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(gitTimeout):
		_ = cmd.Process.Kill()
		<-done
	}
	return strings.TrimSpace(string(out)), err
}

// recentFiles returns the top-n most recently modified files under
// root (ignore-aware walk shared with search_code), newest first,
// as "relpath (age)" lines.
func recentFiles(root string, n int, now time.Time) []string {
	type entry struct {
		rel string
		mod time.Time
	}
	var all []entry
	_ = search.WalkFiles(root, func(path string) error {
		fi, err := os.Stat(path)
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		all = append(all, entry{rel: filepath.ToSlash(rel), mod: fi.ModTime()})
		return nil
	})
	if len(all) == 0 {
		return nil
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mod.After(all[j].mod) })
	if len(all) > n {
		all = all[:n]
	}
	out := make([]string, 0, len(all))
	for _, e := range all {
		out = append(out, e.rel+" ("+age(now.Sub(e.mod))+")")
	}
	return out
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
	lines := splitLines(status)
	if len(lines) <= defaultMaxStatusFiles {
		return lines
	}

	types := make(map[string]int)
	areas := make(map[string]int)
	sample := make([]string, 0, defaultMaxStatusFiles)
	for _, line := range lines {
		code, path := porcelainParts(line)
		types[statusKind(code)]++
		areas[statusArea(path)]++
		if len(sample) < defaultMaxStatusFiles {
			sample = append(sample, path)
		}
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
		"total: " + itoa(len(lines)) + " (" + strings.Join(typeParts, ", ") + ")",
		"areas: " + strings.Join(areaParts, ", "),
		"sample: " + strings.Join(sample, ", ") + " (and " + itoa(len(lines)-len(sample)) + " more)",
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
	parts := strings.Split(path, "/")
	if len(parts) >= 2 && parts[0] == "internal" {
		return strings.Join(parts[:2], "/")
	}
	if len(parts) >= 2 && (parts[0] == "cmd" || parts[0] == "docs" || parts[0] == "test") {
		return parts[0]
	}
	if len(parts) > 1 {
		return parts[0]
	}
	return "root"
}
