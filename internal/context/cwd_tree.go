package context

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CwdTreeLoader produces a depth-limited, file-counted view of
// the home directory. It is designed to be cheap (max 200
// entries, max 3 levels deep) and to never expose file contents.
type CwdTreeLoader struct {
	Home string

	// MaxDepth caps directory recursion. Zero = 3.
	MaxDepth int
	// MaxEntries caps the number of file/dir lines. Zero = 200.
	MaxEntries int
	// SkipDirs is a set of directory names to ignore. The
	// default skips .git, node_modules, vendor, target, dist,
	// build and .supercli (the home for our own state).
	SkipDirs map[string]bool
}

// NewCwdTreeLoader returns a loader with sane defaults.
func NewCwdTreeLoader(home string) *CwdTreeLoader {
	return &CwdTreeLoader{
		Home:        home,
		MaxDepth:    3,
		MaxEntries:  200,
		SkipDirs:    defaultSkipDirs(),
	}
}

func defaultSkipDirs() map[string]bool {
	return map[string]bool{
		".git":          true,
		"node_modules":  true,
		"vendor":        true,
		"target":        true,
		"dist":          true,
		"build":         true,
		".next":         true,
		".cache":        true,
		".supercli":     true,
		"__pycache__":   true,
		".venv":         true,
		"venv":          true,
	}
}

// Name implements Loader.
func (l *CwdTreeLoader) Name() string { return "cwd_tree" }

// Priority is medium. The tree is helpful but lossy.
func (l *CwdTreeLoader) Priority() int { return 40 }

// Load walks the home directory up to MaxDepth and produces a
// compressed view: "src/ (12 files, 4.2k LOC, 3 dirs)".
func (l *CwdTreeLoader) Load() (Source, error) {
	if l.Home == "" {
		return Source{}, fmt.Errorf("context.CwdTreeLoader: home is empty")
	}
	maxDepth := l.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 3
	}
	maxEntries := l.MaxEntries
	if maxEntries <= 0 {
		maxEntries = 200
	}

	type dirInfo struct {
		dirs   int
		files  int
		bytes  int64
	}
	stats := map[string]*dirInfo{}
	rootInfo := &dirInfo{}
	stats[l.Home] = rootInfo

	err := filepath.WalkDir(l.Home, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable entries; do not abort the walk.
			return nil
		}
		if path == l.Home {
			return nil
		}
		rel, _ := filepath.Rel(l.Home, path)
		depth := strings.Count(rel, string(os.PathSeparator)) + 1
		if depth > maxDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if l.SkipDirs[name] {
				return filepath.SkipDir
			}
			// Find parent info.
			parent := filepath.Dir(path)
			if p, ok := stats[parent]; ok {
				p.dirs++
			}
			stats[path] = &dirInfo{}
			return nil
		}
		// File
		parent := filepath.Dir(path)
		if p, ok := stats[parent]; ok {
			p.files++
		}
		info, err := d.Info()
		if err == nil {
			rootInfo.bytes += info.Size()
		}
		return nil
	})
	if err != nil {
		return Source{}, err
	}

	// Render.
	var lines []string
	for path, info := range stats {
		if path == l.Home {
			continue
		}
		rel, _ := filepath.Rel(l.Home, path)
		rel = filepath.ToSlash(rel)
		lines = append(lines, fmt.Sprintf("%s/  (%d files, %d dirs)", rel, info.files, info.dirs))
	}
	// Also add top-level file count.
	topLevel := topLevelCounts(l.Home, l.SkipDirs)
	lines = append([]string{
		fmt.Sprintf("./  (%d files, %d dirs, %d bytes)", topLevel.files, topLevel.dirs, rootInfo.bytes),
	}, lines...)

	sort.Strings(lines)
	if len(lines) > maxEntries {
		lines = lines[:maxEntries]
		lines = append(lines, fmt.Sprintf("… (%d more entries truncated)", maxEntries))
	}

	if len(lines) == 0 {
		return Source{Name: l.Name(), Body: ""}, nil
	}
	body := strings.Join(lines, "\n")
	return Source{
		Name:     l.Name(),
		Body:     body,
		Priority: l.Priority(),
		TokenCap: 800,
	}, nil
}

type count struct {
	files int
	dirs  int
}

func topLevelCounts(home string, skip map[string]bool) count {
	entries, err := os.ReadDir(home)
	if err != nil {
		return count{}
	}
	var c count
	for _, e := range entries {
		if e.IsDir() {
			if skip[e.Name()] {
				continue
			}
			c.dirs++
		} else {
			c.files++
		}
	}
	return c
}
