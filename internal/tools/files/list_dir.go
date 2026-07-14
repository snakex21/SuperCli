// list_dir.go implements the list_dir tool: a tiny, obvious
// "what's in this folder?" primitive. It exists because
// small-tier models otherwise hallucinate a shell `ls`/`dir`
// command (which SuperCli does not expose) when asked to list a
// directory. file_ops has a `list` action too, but its name and
// description bury that capability under move/copy/rename; a
// dedicated, clearly named tool is far more discoverable.
//
// It is pure Go (os.ReadDir, no shelling out) so it works the
// same on Windows, Linux and macOS. Paths go through the
// sandbox; an empty path lists the working directory.
package files

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"supercli/internal/tools/fileops"
)

// ListDirTool lists the entries of a single directory.
type ListDirTool struct {
	BaseDir string
	// MaxEntries caps output so a huge directory can't flood
	// the model's context.
	MaxEntries int
}

// NewListDir returns a ListDirTool rooted at baseDir.
func NewListDir(baseDir string) *ListDirTool {
	if baseDir == "" {
		baseDir = "."
	}
	return &ListDirTool{BaseDir: baseDir, MaxEntries: 500}
}

// Spec returns the Tool descriptor.
func (t *ListDirTool) Spec() Tool {
	return Tool{
		Name:     "list_dir",
		ReadOnly: true,
		Description: "List a folder's contents — the 'ls'/'dir' for SuperCli. " +
			"Use this (not file_ops) for \"what's in this folder?\". " +
			"Empty 'path' lists the working directory; one level, not recursive.",
		Schema: `{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Folder to list. Empty or omitted = current working directory."}
  }
}`,
		Fn: t.Execute,
	}
}

type listDirArgs struct {
	Path string `json:"path"`
}

// Execute lists the directory named by args.Path (default: the
// working directory).
func (t *ListDirTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, err
	}
	var p listDirArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return Result{Err: fmt.Errorf("list_dir: bad args: %w", err)}, err
		}
	}
	path := strings.TrimSpace(p.Path)
	// Some local models serialize an empty path twice and produce a JSON
	// string whose value is literally "\"\"". Treat one redundant quoting
	// layer as transport noise; this avoids an otherwise pointless repair turn
	// while keeping the resolved path inside the normal sandbox.
	if unquoted, err := strconv.Unquote(path); err == nil {
		path = strings.TrimSpace(unquoted)
	} else if path == "''" {
		path = ""
	}
	var dir string
	if path == "" {
		dir = t.BaseDir
	} else {
		resolved, err := resolveSandboxed(t.BaseDir, path)
		if err != nil {
			err = fmt.Errorf("list_dir: %w", err)
			return Result{Err: err}, err
		}
		dir = resolved
	}

	info, err := os.Stat(dir)
	if err != nil {
		err = fmt.Errorf("list_dir: %w", fileops.FileErr(err, dir))
		return Result{Err: err}, err
	}
	if !info.IsDir() {
		return Result{Text: fmt.Sprintf("%s is a file (%d bytes), not a folder.", dir, info.Size())}, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return Result{Err: fmt.Errorf("list_dir: %w", err)}, err
	}
	var lines []string
	truncated := false
	for _, d := range entries {
		if len(lines) >= t.MaxEntries {
			truncated = true
			break
		}
		if d.IsDir() {
			lines = append(lines, d.Name()+"/")
			continue
		}
		size := int64(0)
		if fi, err := d.Info(); err == nil {
			size = fi.Size()
		}
		lines = append(lines, fmt.Sprintf("%s (%d bytes)", d.Name(), size))
	}
	sort.Strings(lines)

	if len(lines) == 0 {
		return Result{Text: fmt.Sprintf("%s is empty.", dir)}, nil
	}
	out := fmt.Sprintf("%s contains %d item(s):\n%s", dir, len(lines), strings.Join(lines, "\n"))
	if truncated {
		out += fmt.Sprintf("\n... (showing first %d of %d entries; list a subfolder to narrow, or use ctx_execute for the full listing)", t.MaxEntries, len(entries))
	}
	return Result{Text: out}, nil
}
