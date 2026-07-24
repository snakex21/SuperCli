package search

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"supercli/internal/system/childproc"
	core "supercli/internal/tools/core"
	"supercli/internal/tools/sandbox"
)

// SearchCode is a simple code-search tool used by the explore
// and review sub-agents. It uses ripgrep (`rg`) when available
// and falls back to filepath.Glob + os.ReadFile on Windows or
// when rg is missing. F4 will replace this with a sembledown
// integration; for F3 the plain grep is enough to validate the
// sub-agent wiring.
//
// Schema:
//
//	{
//	  "query":   string (regex, required),
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
		Description: "Search the codebase for lines matching a regex (read-only). " +
			"Returns file:line:content for each match, up to max results. " +
			"Use to find locations, then read_lines/read_many and patch_file — " +
			"do not keep running alternate searches without a concrete new question. " +
			"Never invent edits from search hits alone.",
		Schema: `{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "regex pattern (Go RE2)"},
				"path":  {"type": "string", "description": "search root, default: cwd"},
				"max":   {"type": "integer", "description": "max results, default 50"}
			},
			"required": ["query"]
		}`,
		Fn: s.run,
	}
}

type searchCodeArgs struct {
	Query string `json:"query"`
	Path  string `json:"path"`
	Max   int    `json:"max"`
}

func (s *SearchCode) run(ctx context.Context, args json.RawMessage) (Result, error) {
	var a searchCodeArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("search_code: bad args: %w", err)}, nil
	}
	if a.Query == "" {
		return Result{Err: fmt.Errorf("search_code: query is empty")}, nil
	}
	if a.Max <= 0 {
		a.Max = 50
	}
	root := a.Path
	if root == "" {
		root = s.WorkDir
	}
	if !filepath.IsAbs(root) {
		// Best-effort: join with WorkDir so relative
		// paths from the model resolve correctly.
		root = filepath.Join(s.WorkDir, root)
	}
	root, err := sandbox.ResolveSafe(s.WorkDir, root)
	if err != nil {
		return Result{Err: fmt.Errorf("search_code: %w", err)}, nil
	}

	if !s.hasRG() {
		return s.fallback(ctx, root, a.Query, a.Max)
	}
	return s.ripgrep(ctx, root, a.Query, a.Max)
}

func (s *SearchCode) hasRG() bool {
	_, err := exec.LookPath("rg")
	return err == nil
}

func (s *SearchCode) ripgrep(ctx context.Context, root, query string, max int) (Result, error) {
	// rg's --max-count is PER FILE, while the tool contract is a
	// GLOBAL cap. The pipe reader below enforces the real limit:
	// it stops after `max` surviving matches and kills rg, so a
	// query hitting thousands of files never buffers the full
	// output in RAM (cmd.Output() used to load everything before
	// trimming to `max` lines).
	args := []string{"--no-heading", "--line-number", "--max-count", fmt.Sprintf("%d", max)}
	for _, dir := range []string{".git", "node_modules", "vendor", "target", "dist", "build", ".next", ".cache", "__pycache__", ".venv", "venv", ".supercli"} {
		args = append(args, "-g", "!"+dir+"/**")
	}
	args = append(args, "--", query, root)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "rg", args...)
	childproc.HideWindow(cmd)
	stderr := core.NewHeadTailBuffer(2048, 1024)
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return s.rgFailed(ctx, root, query, max, err, stderr)
	}
	if err := cmd.Start(); err != nil {
		return s.rgFailed(ctx, root, query, max, err, stderr)
	}

	lines := make([]string, 0, max)
	hitLimit := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if ripgrepPathIsSkipped(line) {
			continue
		}
		lines = append(lines, line)
		if len(lines) == max {
			hitLimit = true
			break
		}
	}
	if hitLimit {
		cancel() // global limit reached: kill rg, drop the rest
	}
	waitErr := cmd.Wait()
	switch {
	case hitLimit:
		// Kill-induced Wait errors are expected here.
		return Result{Text: strings.Join(lines, "\n")}, nil
	case waitErr != nil:
		// rg exits 1 when there are no matches — a valid result,
		// NOT a failure.
		if ee, ok := waitErr.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			break
		}
		return s.rgFailed(ctx, root, query, max, waitErr, stderr)
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
func (s *SearchCode) rgFailed(ctx context.Context, root, query string, max int, rgErr error, stderr *core.HeadTailBuffer) (Result, error) {
	res, err := s.fallback(ctx, root, query, max)
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

func ripgrepPathIsSkipped(line string) bool {
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
	path = strings.ReplaceAll(path, "\\", "/")
	for _, part := range strings.Split(path, "/") {
		if skippedDirs[strings.ToLower(part)] {
			return true
		}
	}
	return false
}

// fallback is a tiny case-insensitive substring search used
// when rg is unavailable. It walks a list of likely code
// extensions and bails out at max matches.
func (s *SearchCode) fallback(ctx context.Context, root, query string, max int) (Result, error) {
	lower := strings.ToLower(query)
	exts := map[string]bool{
		".go": true, ".py": true, ".js": true, ".ts": true,
		".tsx": true, ".jsx": true, ".rs": true, ".java": true,
		".c": true, ".h": true, ".cpp": true, ".hpp": true,
		".md": true, ".txt": true, ".toml": true, ".json": true,
		".yaml": true, ".yml": true, ".sh": true,
	}
	count := 0
	var b strings.Builder
	walk := func(path string) error {
		if count >= max {
			return errStopWalk
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !exts[ext] {
			return nil
		}
		// Scan the file line-by-line instead of materializing a
		// []string of the whole file: constant memory, and the
		// scan stops as soon as the global limit is reached.
		f, err := openFile(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if strings.Contains(strings.ToLower(line), lower) {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				fmt.Fprintf(&b, "%s:%d:%s", path, lineNo, line)
				count++
				if count >= max {
					return errStopWalk
				}
			}
		}
		return nil
	}
	if err := WalkFiles(root, walk); err != nil && err != errStopWalk {
		// A failed scan is an error, not a search result.
		return Result{Err: fmt.Errorf("search_failed walk: %w", err)}, nil
	}
	if b.Len() == 0 {
		return Result{Text: "no matches"}, nil
	}
	return Result{Text: b.String()}, nil
}

var errStopWalk = fmt.Errorf("stop")

// cross-platform hidden guards
var _ = runtime.GOOS
