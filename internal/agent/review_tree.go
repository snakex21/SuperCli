package agent

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Bounds for the post-hoc review tree snapshot (config `review_model`):
// small text files only, so a snapshot of a large tree stays cheap and the
// rendered changes fit one tool-free review prompt.
const (
	reviewSnapshotMaxFile  = 256 << 10
	reviewSnapshotMaxTotal = 16 << 20
	reviewChangeMaxFile    = 12 << 10
	reviewChangeMaxTotal   = 32 << 10
)

// SnapshotTree records small text files under root (dot-directories and
// node_modules skipped). Used as the "before" side of RenderTreeChanges.
func SnapshotTree(root string) map[string]string {
	out := map[string]string{}
	total := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > reviewSnapshotMaxFile || total+int(info.Size()) > reviewSnapshotMaxTotal {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		total += len(data)
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	return out
}

// RenderTreeChanges lists new, modified (full new content) and deleted files
// relative to the before snapshot, bounded for the review prompt.
func RenderTreeChanges(root string, before map[string]string) string {
	after := SnapshotTree(root)
	var paths []string
	for p := range after {
		if old, ok := before[p]; !ok || old != after[p] {
			paths = append(paths, p)
		}
	}
	var deleted []string
	for p := range before {
		if _, ok := after[p]; !ok {
			deleted = append(deleted, p)
		}
	}
	sort.Strings(paths)
	sort.Strings(deleted)
	var b strings.Builder
	for _, p := range paths {
		kind := "modified"
		if _, ok := before[p]; !ok {
			kind = "new"
		}
		body := after[p]
		if len(body) > reviewChangeMaxFile {
			body = body[:reviewChangeMaxFile] + "\n[...truncated...]"
		}
		entry := fmt.Sprintf("### %s (%s, full content)\n```\n%s\n```\n", p, kind, strings.TrimRight(body, "\n"))
		if b.Len()+len(entry) > reviewChangeMaxTotal {
			fmt.Fprintf(&b, "### %s (%s, omitted: size limit)\n", p, kind)
			continue
		}
		b.WriteString(entry)
	}
	for _, p := range deleted {
		fmt.Fprintf(&b, "### %s (deleted)\n", p)
	}
	return b.String()
}
