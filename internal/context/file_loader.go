package context

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileLoader reads project-root files (CLAUDE.md, AGENTS.md,
// README.md, .gitignore) from the home directory and combines
// them into a single "project_notes" source.
//
// Files are loaded in this order; missing files are silently
// skipped. A 8 KiB cap per file is enforced; oversized files
// have their tail truncated with a marker.
type FileLoader struct {
	Home string

	// MaxFileBytes caps the size of each individual file. Zero
	// means use the default (8 KiB).
	MaxFileBytes int
}

// section is one labelled chunk of a project_notes source.
type section struct {
	label string
	body  string
}

// NewFileLoader returns a loader rooted at home. home is the
// resolved SuperCli home (cwd by default).
func NewFileLoader(home string) *FileLoader {
	return &FileLoader{Home: home, MaxFileBytes: 8 * 1024}
}

// Name implements Loader.
func (l *FileLoader) Name() string { return "project_notes" }

// Priority is high because the model is expected to honour
// project notes above memory_recent but below the system prompt.
func (l *FileLoader) Priority() int { return 90 }

// Load reads the configured files and returns a single Source.
func (l *FileLoader) Load() (Source, error) {
	if l.Home == "" {
		return Source{}, fmt.Errorf("context.FileLoader: home is empty")
	}
	maxBytes := l.MaxFileBytes
	if maxBytes <= 0 {
		maxBytes = 8 * 1024
	}

	// (display name, filename)
	files := []struct {
		Label string
		Path  string
	}{
		{"CLAUDE.md", "CLAUDE.md"},
		{"AGENTS.md", "AGENTS.md"},
		{"README.md", "README.md"},
		{".gitignore", ".gitignore"},
	}

	var sections []section
	for _, f := range files {
		full := filepath.Join(l.Home, f.Path)
		data, err := os.ReadFile(full)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Source{}, fmt.Errorf("context.FileLoader: read %s: %w", f.Path, err)
		}
		if len(data) > maxBytes {
			// Truncate to maxBytes and add a marker.
			data = data[:maxBytes]
			data = append(data, []byte("\n\n[…truncated, file exceeds 8 KiB cap…]")...)
		}
		body := strings.TrimRight(string(data), "\n")
		if body == "" {
			continue
		}
		sections = append(sections, section{label: f.Label, body: body})
	}

	if len(sections) == 0 {
		// No files present: return an empty source so callers
		// can decide to skip it.
		return Source{Name: l.Name(), Body: ""}, nil
	}

	var b strings.Builder
	for _, s := range sections {
		fmt.Fprintf(&b, "### %s\n", s.label)
		b.WriteString(s.body)
		b.WriteString("\n\n")
	}
	return Source{
		Name:     l.Name(),
		Body:     b.String(),
		Priority: l.Priority(),
		TokenCap: 2400,
		Meta:     fileNames(sections),
	}, nil
}

func fileNames(ss []section) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.label)
	}
	sort.Strings(out)
	return out
}
