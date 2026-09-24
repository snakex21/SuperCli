package files

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"supercli/internal/tools/search"
)

// listTree visits breadth first so a large first subtree cannot hide the other
// top-level modules. MaxEntries is shared by the whole listing, not each folder.
func (t *ListDirTool) listTree(ctx context.Context, root string, depth int) (Result, error) {
	type folder struct {
		rel   string
		level int
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Result{Err: err}, err
	}
	queue := []folder{{level: 1}}
	lines := make([]string, 0)
	truncated := false
	depthLimited := false
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return Result{Err: err}, err
		}
		current := queue[0]
		queue = queue[1:]
		path := filepath.Join(absRoot, current.rel)
		if current.rel != "" {
			// Recheck queued directories through the same sandbox as explicit paths.
			resolved, err := resolveSandboxed(t.BaseDir, path)
			if err != nil {
				err = fmt.Errorf("list_dir: %w", err)
				return Result{Err: err}, err
			}
			path = resolved
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			err = fmt.Errorf("list_dir: %w", err)
			return Result{Err: err}, err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return Result{Err: err}, err
			}
			if len(lines) >= t.MaxEntries {
				truncated = true
				break
			}
			rel := filepath.Join(current.rel, entry.Name())
			label := filepath.ToSlash(rel)
			switch {
			case entry.Type()&os.ModeSymlink != 0:
				lines = append(lines, label+" (symlink; not expanded)")
			case entry.IsDir():
				label += "/"
				if search.IsSkippedSubtree(absRoot, filepath.Join(absRoot, rel)) {
					label += " (not expanded)"
				} else if current.level < depth {
					queue = append(queue, folder{rel: rel, level: current.level + 1})
				} else {
					label += " (depth limit)"
					depthLimited = true
				}
				lines = append(lines, label)
			default:
				info, err := entry.Info()
				if err != nil {
					err = fmt.Errorf("list_dir: %w", err)
					return Result{Err: err}, err
				}
				lines = append(lines, fmt.Sprintf("%s (%d bytes)", label, info.Size()))
			}
		}
		if truncated {
			break
		}
	}
	if len(lines) == 0 {
		return Result{Text: fmt.Sprintf("%s is empty.", root)}, nil
	}
	sort.Strings(lines)
	scope := fmt.Sprintf("within depth %d; deeper folders marked", depth)
	if !depthLimited && !truncated {
		scope = "complete tree; excluded entries marked"
	}
	out := fmt.Sprintf("%s contains %d item(s) (%s):\n%s", root, len(lines), scope, strings.Join(lines, "\n"))
	if truncated {
		out += fmt.Sprintf("\n... (entry limit %d reached; listing incomplete; narrow path)", t.MaxEntries)
	}
	return Result{Text: out}, nil
}
