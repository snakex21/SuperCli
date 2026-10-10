package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/tools/sandbox"
)

type toolFileAccess struct {
	path  string
	write bool
	info  os.FileInfo
}

// toolConflictWaves returns contiguous waves that may execute concurrently.
// The boolean is false when any call has an unknown mutation/resource shape;
// callers then keep the conservative sequential behavior.
func (l *Loop) toolConflictWaves(toolCalls []llm.ToolCall) ([][]llm.ToolCall, bool) {
	return l.toolConflictWavesContext(context.Background(), toolCalls)
}

// Download export permission belongs to this invocation. Resolve its targets
// with the same context as execution, rather than falling back to sequential
// dispatch just because a human requested a folder outside the workspace.
func (l *Loop) toolConflictWavesContext(ctx context.Context, toolCalls []llm.ToolCall) ([][]llm.ToolCall, bool) {
	if len(toolCalls) < 2 || l.baseDir == "" {
		return nil, false
	}
	accesses := make([][]toolFileAccess, len(toolCalls))
	hasMutation := false
	for i, call := range toolCalls {
		tool, ok := l.registry.Get(call.Name)
		if !ok {
			return nil, false
		}
		acc, known := fileAccessesForCall(call)
		if !known {
			// Unknown read-only tools are harmless relative to one another, but
			// in a mixed batch they may read a file/process state that a mutation
			// is changing. Kimi's safe rule is the same: unknown resource shape is
			// a barrier.
			if !tool.ReadOnly {
				return nil, false
			}
			accesses[i] = nil
			continue
		}
		accesses[i] = acc
		if !tool.ReadOnly {
			hasMutation = true
		}
	}
	if !hasMutation {
		return nil, false
	}
	// A read-only call with an unknown footprint becomes a barrier in a mixed
	// batch. This preserves correctness over speculative parallelism.
	for i, call := range toolCalls {
		tool, _ := l.registry.Get(call.Name)
		if tool.ReadOnly && accesses[i] == nil {
			return nil, false
		}
	}

	// Compare actual tool targets, not argument spelling: relative/absolute
	// paths and symlink parents may point to the same file. Scope reuse to this
	// batch so renames or link changes cannot leave a stale identity cache.
	type resolutionKey struct {
		path     string
		download bool
	}
	resolved := make(map[resolutionKey]toolFileAccess)
	parents := make(map[string]string)
	for i, acc := range accesses {
		for j, access := range acc {
			key := resolutionKey{path: access.path, download: toolCalls[i].Name == "web_download"}
			target, found := resolved[key]
			if !found {
				var err error
				if key.download {
					target, err = l.resolveDownloadFileAccess(ctx, access.path)
				} else {
					target, err = l.resolveToolFileAccess(access.path, parents)
				}
				if err != nil {
					return nil, false
				}
				resolved[key] = target
			}
			target.write = access.write
			accesses[i][j] = target
		}
	}

	var waves [][]llm.ToolCall
	var current []llm.ToolCall
	var currentAccess []toolFileAccess
	for i, call := range toolCalls {
		acc := accesses[i]
		if len(current) > 0 && accessesConflict(currentAccess, acc) {
			waves = append(waves, current)
			current = nil
			currentAccess = nil
		}
		current = append(current, call)
		currentAccess = append(currentAccess, acc...)
	}
	if len(current) > 0 {
		waves = append(waves, current)
	}
	return waves, true
}

// Keep export authorization specific to web_download: a sibling read/edit tool
// cannot acquire the grant from a shared path-identity cache. The resolver pins
// junction roots and canonicalizes missing destinations exactly as the tool does.
func (l *Loop) resolveDownloadFileAccess(ctx context.Context, path string) (toolFileAccess, error) {
	full, err := sandbox.ResolveDownloadDestination(ctx, l.baseDir, path)
	if err != nil {
		return toolFileAccess{}, err
	}
	info, err := os.Stat(full)
	if err != nil && !os.IsNotExist(err) {
		return toolFileAccess{}, err
	}
	return toolFileAccess{path: normalizeToolPath(full), info: info}, nil
}

// Resolve a shared parent once per batch rather than walking the whole project
// path for every sibling. Lstat still checks the final component: a file symlink
// must be resolved too, while a nonexistent target keeps its canonical parent.
func (l *Loop) resolveToolFileAccess(path string, parents map[string]string) (toolFileAccess, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(l.baseDir, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return toolFileAccess{}, err
	}
	parent := filepath.Dir(path)
	resolved, ok := parents[parent]
	if !ok {
		var err error
		resolved, err = sandbox.ResolveSafe(l.baseDir, parent)
		if err != nil {
			return toolFileAccess{}, err
		}
		parents[parent] = resolved
	}
	full := filepath.Join(resolved, filepath.Base(path))
	info, err := os.Lstat(full)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		full, err = sandbox.ResolveSafe(l.baseDir, path)
		if err != nil {
			return toolFileAccess{}, err
		}
		info, err = os.Stat(full)
	}
	if err != nil && !os.IsNotExist(err) {
		return toolFileAccess{}, err
	}
	return toolFileAccess{path: normalizeToolPath(full), info: info}, nil
}

func fileAccessesForCall(call llm.ToolCall) ([]toolFileAccess, bool) {
	// Unknown resource shapes stay conservative without copying/parsing arguments.
	switch call.Name {
	case "write_file", "patch_file", "create_file", "make_dir", "trash", "edit_docx", "edit_xlsx", "web_download",
		"read_lines", "read_context", "list_dir", "read_image", "read_docx", "read_pdf", "read_xlsx",
		"read_zip", "copy", "move":
	default:
		return nil, false
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return nil, false
	}
	get := func(key string) (string, bool) {
		raw, ok := args[key]
		if !ok {
			return "", false
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil || strings.TrimSpace(s) == "" {
			return "", false
		}
		return s, true
	}
	one := func(key string, write bool) ([]toolFileAccess, bool) {
		p, ok := get(key)
		if !ok {
			return nil, false
		}
		return []toolFileAccess{{path: p, write: write}}, true
	}

	switch call.Name {
	case "write_file", "patch_file", "create_file", "make_dir", "trash", "edit_docx", "edit_xlsx", "web_download":
		return one("path", true)
	case "read_lines", "read_context":
		return one("file", false)
	case "list_dir", "read_image", "read_docx", "read_pdf", "read_xlsx":
		return one("path", false)
	case "read_zip":
		var action string
		if raw, present := args["action"]; present {
			if value := strings.TrimSpace(string(raw)); len(value) == 0 || value[0] != '"' {
				return nil, false
			}
			if err := json.Unmarshal(raw, &action); err != nil {
				return nil, false
			}
		}
		if action == "" || action == "list" {
			return one("path", false)
		}
		if action == "read" {
			if raw, present := args["paths"]; present {
				if _, hasPath := args["path"]; hasPath {
					return nil, false
				}
				var paths []string
				if json.Unmarshal(raw, &paths) != nil || len(paths) < 1 || len(paths) > 16 {
					return nil, false
				}
				accesses := make([]toolFileAccess, 0, len(paths))
				for _, path := range paths {
					if strings.TrimSpace(path) == "" {
						return nil, false
					}
					accesses = append(accesses, toolFileAccess{path: path})
				}
				return accesses, true
			}
			return one("path", false)
		}
		if action != "extract" {
			return nil, false
		}
		src, ok1 := get("path")
		dst, ok2 := get("target_dir")
		if !ok1 || !ok2 {
			// The default destination depends on the tool instance and current time.
			return nil, false
		}
		return []toolFileAccess{{path: src, write: false}, {path: dst, write: true}}, true
	case "copy":
		src, ok1 := get("src")
		dst, ok2 := get("dest")
		if !ok1 || !ok2 {
			return nil, false
		}
		return []toolFileAccess{{path: src, write: false}, {path: dst, write: true}}, true
	case "move":
		src, ok1 := get("src")
		dst, ok2 := get("dest")
		if !ok1 || !ok2 {
			return nil, false
		}
		return []toolFileAccess{{path: src, write: true}, {path: dst, write: true}}, true
	default:
		return nil, false
	}
}

func accessesConflict(a, b []toolFileAccess) bool {
	for _, x := range a {
		for _, y := range b {
			if !(x.write || y.write) {
				continue
			}
			if toolPathsOverlap(x.path, y.path) || (x.info != nil && y.info != nil && os.SameFile(x.info, y.info)) {
				return true
			}
		}
	}
	return false
}

func normalizeToolPath(p string) string {
	p = filepath.ToSlash(filepath.Clean(p))
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return strings.TrimSuffix(p, "/")
}

func toolPathsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	if a == "" || b == "" {
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
