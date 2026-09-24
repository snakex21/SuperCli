package search

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"supercli/internal/system/childproc"
	core "supercli/internal/tools/core"
	"supercli/internal/tools/sandbox"
)

// SearchCode is a simple code-search tool used by the explore
// and review sub-agents. It uses ripgrep (`rg`) when available
// and a bounded Go text scanner otherwise.
//
// Schema:
//
//	{
//	  "query":   string (regex; omit with include to find file paths),
//	  "path":    string (default: cwd, search root),
//	  "max":     int    (default: 50, max results)
//	}
type SearchCode struct {
	// WorkDir is the root for relative paths. Empty means cwd.
	WorkDir string
	// MaxLimit caps results. Zero means 50.
	MaxLimit int
}

// NewSearchCode returns a SearchCode tool bound to the given
// working directory.
func NewSearchCode(workDir string) *SearchCode {
	if workDir == "" {
		workDir = "."
	}
	return &SearchCode{WorkDir: workDir}
}

// Spec returns the tools.Tool description for the registry.
func (s *SearchCode) Spec() Tool {
	return Tool{
		Name:     "search_code",
		ReadOnly: true,
		Description: "Search code (RE2; (?i) ignores case) with file/line references. " +
			"With include and no query, list paths without reading files. Skips build/dependency dirs.",
		Schema: `{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "content regex; omit to find files"},
				"path":  {"type": "string", "description": "search root, default: cwd"},
                "include": {"type": "string", "description": "glob relative to path: *.go, src/**/*.ts, *.{zig,go}; ** spans 0+ dirs"},
				"max":   {"type": "integer", "description": "max results, default 50"},
				"context": {"type": "integer", "minimum": 0, "maximum": 20, "description": "surrounding lines; default auto for up to 3 hits, 0 = locations"}
			}
		}`,
		Fn: s.run,
	}
}

type searchCodeArgs struct {
	Query   string `json:"query"`
	Path    string `json:"path"`
	Max     int    `json:"max"`
	Context *int   `json:"context"`
	Include string `json:"include"`
}

func (s *SearchCode) run(ctx context.Context, args json.RawMessage) (Result, error) {
	var a searchCodeArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("search_code: bad args: %w", err)}, nil
	}
	if a.Query == "" && a.Include == "" {
		return Result{Err: fmt.Errorf("search_code: provide query for content or include for file paths")}, nil
	}
	if a.Max <= 0 {
		a.Max = 50
	}
	root, err := sandbox.ResolveSafe(s.WorkDir, a.Path)
	if err != nil {
		return Result{Err: fmt.Errorf("search_code: %w", err)}, nil
	}

	radius := 0
	if a.Context != nil {
		radius = *a.Context
	}
	if radius < 0 || radius > maxSearchContextRadius {
		return Result{Err: fmt.Errorf("search_code: context must be between 0 and %d", maxSearchContextRadius)}, nil
	}
	include, err := compileSearchGlob(a.Include)
	if err != nil {
		return Result{Err: fmt.Errorf("search_code: %w", err)}, nil
	}
	if a.Query == "" {
		if radius != 0 {
			return Result{Err: fmt.Errorf("search_code: context requires a content query")}, nil
		}
		return s.findFiles(ctx, root, include, a.Max)
	}
	autoContext := a.Context == nil
	if autoContext {
		radius = 4
	}
	preview := &searchContext{radius: radius, include: include, query: a.Query}
	var result Result
	rg := s.rgPath()
	if rg == "" {
		result, err = s.fallback(ctx, root, a.Query, a.Max, preview)
	} else {
		result, err = s.ripgrep(ctx, rg, root, a.Query, a.Max, preview)
	}
	if err == nil {
		result = s.previewSearchHits(result, preview, a.Query)
	}
	if err == nil && result.Err == nil && radius > 0 {
		// Sparse searches often locate a declaration without the body that
		// answers the question. Offer a small neighborhood in the same call;
		// broad/limited searches retain the compact location-only output. A long
		// matching line already exceeds the auto byte cap, so skip a context
		// reread whose result would be discarded.
		if !autoContext || (len(preview.hits) <= 3 && preview.limit == 0 && len(preview.longLines) == 0) {
			expanded := s.renderSearchContext(ctx, preview, result)
			if !autoContext || len(expanded.Text) <= 2048 || expanded.Err != nil {
				result = expanded
			}
		}
	}
	return result, err
}

// hasRG reports whether a ripgrep binary is reachable: on
// PATH first, then bundled next to the running executable
// (the same locations ctxexec resolves for `rg` commands,
// so dropping rg.exe next to supercli.exe upgrades both
// paths at once).
func (s *SearchCode) hasRG() bool {
	return s.rgPath() != ""
}

func (s *SearchCode) rgPath() string {
	if path, err := exec.LookPath("rg"); err == nil {
		return path
	}
	executable, err := os.Executable()
	if err != nil || strings.TrimSpace(executable) == "" {
		return ""
	}
	base := filepath.Dir(executable)
	for _, rel := range []string{
		"rg.exe", "rg",
		filepath.Join("bin", "rg.exe"), filepath.Join("bin", "rg"),
		filepath.Join("tools", "rg.exe"), filepath.Join("tools", "rg"),
		filepath.Join("bin", "tools", "rg.exe"), filepath.Join("bin", "tools", "rg"),
	} {
		candidate := filepath.Join(base, rel)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || !info.Mode().IsRegular() {
			continue
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
			continue
		}
		return candidate
	}
	return ""
}

func (s *SearchCode) ripgrep(ctx context.Context, rg, root, query string, max int, previews ...*searchContext) (Result, error) {
	// rg's --max-count is PER FILE, while the tool contract is a
	// GLOBAL cap. The pipe reader below enforces the real limit:
	// it stops after `max` surviving matches and kills rg, so a
	// query hitting thousands of files never buffers the full
	// output in RAM (cmd.Output() used to load everything before
	// trimming to `max` lines).
	args := []string{"--no-heading", "--with-filename", "--color=never", "--line-number", "--max-count", fmt.Sprintf("%d", max)}
	if len(previews) > 0 && previews[0] != nil {
		args = append(args, "--null")
	}
	if len(previews) > 0 && previews[0] != nil && previews[0].include != nil {
		args = append(args, "--glob", previews[0].include.pattern)
	}
	dirs := make([]string, 0, len(skippedDirs))
	for dir := range skippedDirs {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		args = append(args, "-g", "!"+dir+"/**")
	}
	if !pathContainsAgentWorktree(root) {
		args = append(args, "--iglob", "!**/.claude/worktrees/**")
	}
	args = append(args, "--", query, root)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, rg, args...)
	childproc.HideWindow(cmd)
	if len(previews) > 0 && previews[0] != nil && previews[0].include != nil {
		cmd.Dir = root
		if info, e := os.Stat(root); e == nil && !info.IsDir() {
			cmd.Dir = filepath.Dir(root)
		}
	}
	stderr := core.NewHeadTailBuffer(2048, 1024)
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return s.rgFailed(ctx, root, query, max, err, stderr, previews...)
	}
	if err := cmd.Start(); err != nil {
		return s.rgFailed(ctx, root, query, max, err, stderr, previews...)
	}

	lines := make([]string, 0, min(max, 50))
	hitLimit := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if len(previews) > 0 && previews[0] != nil {
			path, number, content, ok := parseContextSearchHit(line)
			if !ok {
				cancel()
				_ = cmd.Wait()
				return s.rgFailed(ctx, root, query, max, fmt.Errorf("invalid context search record"), stderr, previews...)
			}
			if searchPathIsSkipped(root, path) || !searchFileIncluded(previews, root, path) {
				continue
			}
			captureSearchHit(previews, path, number, content)
			lines = append(lines, fmt.Sprintf("%s:%d:%s", s.displayPath(path), number, content))
		} else {
			if ripgrepPathIsSkipped(root, line) {
				continue
			}
			lines = append(lines, s.displaySearchLine(line))
		}
		if len(lines) == max {
			hitLimit = true
			break
		}
	}
	scanErr := scanner.Err()
	if hitLimit || scanErr != nil {
		cancel() // Stop before Wait: an unread pipe may otherwise block rg.
	}
	waitErr := cmd.Wait()
	switch {
	case ctx.Err() != nil:
		return Result{Err: ctx.Err()}, nil
	case scanErr != nil:
		return s.rgFailed(ctx, root, query, max, scanErr, stderr, previews...)
	case hitLimit:
		// Kill-induced Wait errors are expected here.
		return searchLimitedResult(strings.Join(lines, "\n"), max, previews), nil
	case waitErr != nil:
		// rg exits 1 when there are no matches — a valid result,
		// NOT a failure.
		if ee, ok := waitErr.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			break
		}
		return s.rgFailed(ctx, root, query, max, waitErr, stderr, previews...)
	}
	if len(lines) == 0 {
		return Result{Text: "no matches"}, nil
	}
	return Result{Text: strings.Join(lines, "\n")}, nil
}

// rgFailed handles a real ripgrep failure (bad pattern, crash —
// not "no matches"). The failure must never be presented as a
// successful search result: try the Go fallback scanner, and if
// that also fails return a structured search_failed error.
func (s *SearchCode) rgFailed(ctx context.Context, root, query string, max int, rgErr error, stderr *core.HeadTailBuffer, previews ...*searchContext) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, nil
	}
	if len(previews) > 0 && previews[0] != nil {
		previews[0].records = nil
		previews[0].hits = nil // discard partial rg output before fallback
		previews[0].longLines = nil
		previews[0].limit = 0
	}
	res, err := s.fallback(ctx, root, query, max, previews...)
	if err == nil && res.Err == nil {
		return res, nil
	}
	msg := fmt.Sprintf("search_failed rg: %v", rgErr)
	if detail := strings.TrimSpace(stderr.String()); detail != "" {
		msg += "\nrg stderr:\n" + detail
	}
	if res.Err != nil {
		msg += fmt.Sprintf("\nfallback scanner: %v", res.Err)
	}
	return Result{Err: core.SelfContainedErr(fmt.Errorf("%s", msg))}, nil
}

func ripgrepPathIsSkipped(root, line string) bool {
	path := line
	for i := 0; i < len(line); i++ {
		if line[i] != ':' {
			continue
		}
		j := i + 1
		for j < len(line) && line[j] >= '0' && line[j] <= '9' {
			j++
		}
		if j > i+1 && j < len(line) && line[j] == ':' {
			path = line[:i]
			break
		}
	}
	return searchPathIsSkipped(root, path)
}

func searchPathIsSkipped(root, path string) bool {
	// Ignore directories inside the requested root, not its ancestors.
	// A workspace can itself live under a folder named .tmp or build.
	previous := ""
	if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		path = rel
		previous = filepath.Base(root)
	}
	path = strings.ReplaceAll(path, "\\", "/")
	for _, part := range strings.Split(path, "/") {
		if skippedDirs[strings.ToLower(part)] || isAgentWorktreeDir(previous, part) {
			return true
		}
		previous = part
	}
	return false
}

var errStopWalk = fmt.Errorf("stop")

// cross-platform hidden guards
var _ = runtime.GOOS
