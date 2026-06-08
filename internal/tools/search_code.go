package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
		Name: "search_code",
		Description: "Search the codebase for lines matching a regex. " +
			"Returns file:line:content for each match, up to max results. " +
			"Use this before reading files to find relevant locations.",
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
	cmd := exec.CommandContext(ctx, "rg", "--no-heading", "--line-number",
		"--max-count", fmt.Sprintf("%d", max),
		"--", query, root)
	out, err := cmd.Output()
	if err != nil {
		// rg exits 1 when no matches — not an error.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return Result{Text: "no matches"}, nil
		}
		return Result{Text: s.fallbackErr(err, out)}, nil
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return Result{Text: "no matches"}, nil
	}
	return Result{Text: text}, nil
}

func (s *SearchCode) fallbackErr(err error, out []byte) string {
	return fmt.Sprintf("ripgrep failed: %v\n%s", err, strings.TrimSpace(string(out)))
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
		lines := readLines(path)
		for i, line := range lines {
			if strings.Contains(strings.ToLower(line), lower) {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				fmt.Fprintf(&b, "%s:%d:%s", path, i+1, line)
				count++
				if count >= max {
					return errStopWalk
				}
			}
		}
		return nil
	}
	if err := walkFiles(root, walk); err != nil && err != errStopWalk {
		return Result{Text: fmt.Sprintf("search_code: walk failed: %v", err)}, nil
	}
	if b.Len() == 0 {
		return Result{Text: "no matches"}, nil
	}
	return Result{Text: b.String()}, nil
}

var errStopWalk = fmt.Errorf("stop")

func readLines(path string) []string {
	f, err := openFile(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		out = append(out, scanner.Text())
	}
	return out
}

// cross-platform hidden guards
var _ = runtime.GOOS
