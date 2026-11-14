package preflight

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"supercli/internal/tools/search"
)

const (
	// LayoutBudget caps the optional project-layout section (tokens).
	LayoutBudget = 250
	// layoutListMax lists every file (grouped by folder) up to this many
	// files. Agent sessions on a small project almost always start with a
	// list_dir round trip; the whole layout costs fewer tokens than that
	// turn re-sending the conversation.
	layoutListMax = 60
	// layoutAreaMax caps the folder summary of a larger project.
	layoutAreaMax = 12
)

// layoutSection renders the ignore-aware project layout below root: every
// file grouped by folder for a small project, otherwise the root files and
// the biggest top-level folders with their file counts. It uses the same
// bounded walk as the no-git fallback, so it never starts a subprocess.
func layoutSection(ctx context.Context, root string) (string, []string) {
	scanCtx, cancel := context.WithTimeout(ctx, fallbackTimeout)
	defer cancel()
	var paths []string
	complete, err := search.WalkFileEntriesBounded(scanCtx, root, defaultMaxScanEntries, func(path string, d fs.DirEntry) error {
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if len(paths) == 0 {
		return "", nil
	}
	sort.Strings(paths)
	complete = complete && err == nil
	if complete && len(paths) <= layoutListMax {
		return "files (" + strconv.Itoa(len(paths)) + "):", groupedLayoutLines(paths)
	}
	header := "layout (" + strconv.Itoa(len(paths)) + " files):"
	if !complete {
		header = "layout (partial scan, " + strconv.Itoa(len(paths)) + "+ files):"
	}
	return header, summaryLayoutLines(paths)
}

func groupedLayoutLines(paths []string) []string {
	byDir := map[string][]string{}
	var dirs []string
	for _, p := range paths {
		dir, name := ".", p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			dir, name = p[:i], p[i+1:]
		}
		if _, ok := byDir[dir]; !ok {
			dirs = append(dirs, dir)
		}
		byDir[dir] = append(byDir[dir], name)
	}
	sort.Strings(dirs)
	lines := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		lines = append(lines, dir+"/: "+strings.Join(byDir[dir], " "))
	}
	return lines
}

func summaryLayoutLines(paths []string) []string {
	counts := map[string]int{}
	var tops, rootFiles []string
	for _, p := range paths {
		top, _, nested := strings.Cut(p, "/")
		if !nested {
			rootFiles = append(rootFiles, p)
			continue
		}
		if counts[top] == 0 {
			tops = append(tops, top)
		}
		counts[top]++
	}
	sort.SliceStable(tops, func(i, j int) bool { return counts[tops[i]] > counts[tops[j]] })
	var lines []string
	if len(rootFiles) > 0 {
		if len(rootFiles) > layoutAreaMax {
			rootFiles = append(rootFiles[:layoutAreaMax:layoutAreaMax], "…")
		}
		lines = append(lines, "./: "+strings.Join(rootFiles, " "))
	}
	for i, top := range tops {
		if i == layoutAreaMax {
			lines = append(lines, "… "+strconv.Itoa(len(tops)-layoutAreaMax)+" more folders")
			break
		}
		lines = append(lines, top+"/ ("+strconv.Itoa(counts[top])+" files)")
	}
	return lines
}

// appendLayout adds the layout section to block (a Build result, possibly
// empty) under LayoutBudget, trimming whole lines from the end.
func appendLayout(ctx context.Context, block, root string) string {
	header, lines := layoutSection(ctx, root)
	if len(lines) == 0 {
		return block
	}
	if block == "" {
		block = "Repo state (auto-collected):"
	}
	section := header
	added := false
	for _, ln := range lines {
		cand := section + "\n" + ln
		if EstimateTokens(cand) > LayoutBudget {
			break
		}
		section = cand
		added = true
	}
	if !added {
		return block
	}
	return block + "\n" + section
}
