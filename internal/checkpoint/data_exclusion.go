package checkpoint

import (
	"path"
	"runtime"
	"strings"
)

// The pattern is anchored to the actual data root. Escape Git ignore syntax so
// a custom data directory cannot hide similarly named source directories.
func escapeExcludePath(p string) string {
	return strings.NewReplacer("\\", "\\\\", "*", "\\*", "?", "\\?", "[", "\\[", "]", "\\]", "!", "\\!", "#", "\\#", " ", "\\ ").Replace(p)
}

func (m *Manager) applicationDataPath(p string) bool {
	if m.excludedDataRel == "" {
		return false
	}
	p = path.Clean(p)
	prefix := m.excludedDataRel
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
		prefix = strings.ToLower(prefix)
	}
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}

// Old checkpoints remain useful for source undo/redo, but application state
// must not be restored or cause conflicts when a historical checkpoint used
// incomplete ignore rules. Snapshot objects are retained unchanged.
func (m *Manager) filterApplicationDataRecords() {
	if m.excludedDataRel == "" {
		return
	}
	records := m.records[:0]
	for _, r := range m.records {
		original := len(r.Files)
		files := r.Files[:0]
		for _, p := range r.Files {
			if !m.applicationDataPath(p) {
				files = append(files, p)
			}
		}
		r.Files = files
		changes := r.Changes[:0]
		for _, c := range r.Changes {
			if !m.applicationDataPath(c.Path) {
				changes = append(changes, c)
			}
		}
		r.Changes = changes
		if original > 0 && len(r.Files) == 0 {
			continue
		}
		records = append(records, r)
	}
	m.records = records
}
