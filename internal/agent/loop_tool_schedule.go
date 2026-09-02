package agent

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"

	"supercli/internal/llm"
)

type toolFileAccess struct {
	path  string
	write bool
}

// toolConflictWaves returns contiguous waves that may execute concurrently.
// The boolean is false when any call has an unknown mutation/resource shape;
// callers then keep the conservative sequential behavior.
func (l *Loop) toolConflictWaves(toolCalls []llm.ToolCall) ([][]llm.ToolCall, bool) {
	if len(toolCalls) < 2 {
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

func fileAccessesForCall(call llm.ToolCall) ([]toolFileAccess, bool) {
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
		return normalizeToolPath(s), true
	}
	one := func(key string, write bool) ([]toolFileAccess, bool) {
		p, ok := get(key)
		if !ok {
			return nil, false
		}
		return []toolFileAccess{{path: p, write: write}}, true
	}

	switch call.Name {
	case "write_file", "patch_file", "create_file", "make_dir", "trash", "edit_docx", "edit_xlsx":
		return one("path", true)
	case "read_lines", "read_context":
		return one("file", false)
	case "list_dir", "read_image", "read_docx", "read_pdf", "read_xlsx", "read_zip":
		return one("path", false)
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
			if toolPathsOverlap(x.path, y.path) {
				return true
			}
		}
	}
	return false
}

func normalizeToolPath(p string) string {
	p = filepath.ToSlash(filepath.Clean(strings.TrimSpace(p)))
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
