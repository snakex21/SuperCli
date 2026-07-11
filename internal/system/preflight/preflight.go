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
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/childproc"
	"supercli/internal/tools/search"
)

// DefaultBudget is the hard token cap of the block. Sections are
// added most-important-first and trimmed line by line, so the least
// important content (old commits, extra files) is cut first.
const DefaultBudget = 800

const (
	defaultMaxCommits = 8
	defaultMaxFiles   = 10
	gitTimeout        = 5 * time.Second
)

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
		if branch, err := runGit(root, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && branch != "" {
			gitOK = true
			head, _ := runGit(root, "log", "-1", "--format=%h %s")
			id := "branch: " + branch
			if head != "" {
				id += "\nHEAD: " + head
			}
			secs = append(secs, section{lines: strings.Split(id, "\n")})
			if status, err := runGit(root, "status", "--porcelain"); err == nil {
				if status == "" {
					secs = append(secs, section{lines: []string{"working tree clean"}})
				} else {
					secs = append(secs, section{header: "uncommitted changes:", lines: splitLines(status)})
				}
			}
			if lg, err := runGit(root, "log", "--oneline", "-"+itoa(defaultMaxCommits)); err == nil && lg != "" {
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
