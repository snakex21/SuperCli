package search

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// One bounded positive glob, relative to the search root. A basename-only glob
// matches at any depth. ** spans directories; braces expand a small set of
// alternatives. It is compiled once per call, never stored across workers.
type searchGlob struct {
	pattern      string
	alternatives [][]string
}

func compileSearchGlob(pattern string) (*searchGlob, error) {
	if pattern == "" {
		return nil, nil
	}
	if len(pattern) > 512 || strings.TrimSpace(pattern) == "" || strings.HasPrefix(pattern, "!") || strings.HasPrefix(pattern, "/") || strings.ContainsAny(pattern, "\\:") {
		return nil, fmt.Errorf("include must be one positive relative glob using /, up to 512 bytes")
	}
	patterns := []string{strings.TrimPrefix(pattern, "./")}
	for i := 0; i < len(patterns); {
		p := patterns[i]
		left := strings.IndexByte(p, '{')
		if left < 0 {
			if strings.Contains(p, "}") {
				return nil, fmt.Errorf("include: unmatched brace")
			}
			i++
			continue
		}
		right := strings.IndexByte(p[left+1:], '}')
		if right < 0 {
			return nil, fmt.Errorf("include: unmatched brace")
		}
		right += left + 1
		inner := p[left+1 : right]
		if strings.Contains(inner, "{") {
			return nil, fmt.Errorf("include: nested braces are not supported")
		}
		choices := strings.Split(inner, ",")
		if len(choices) < 2 || len(patterns)-1+len(choices) > 32 {
			return nil, fmt.Errorf("include: use at most 32 brace alternatives")
		}
		expanded := make([]string, 0, len(choices))
		for _, choice := range choices {
			if choice == "" {
				return nil, fmt.Errorf("include: empty brace alternative")
			}
			expanded = append(expanded, p[:left]+choice+p[right+1:])
		}
		patterns = append(append(append([]string{}, patterns[:i]...), expanded...), patterns[i+1:]...)
	}
	g := &searchGlob{pattern: strings.TrimPrefix(pattern, "./")}
	for _, p := range patterns {
		if strings.Contains(p, ",") {
			return nil, fmt.Errorf("include accepts one glob; use {a,b} for alternatives")
		}
		parts := strings.Split(p, "/")
		for _, part := range parts {
			if part == "" || part == ".." || part == "." {
				return nil, fmt.Errorf("include must be a relative file glob")
			}
			if part != "**" && strings.Contains(part, "**") {
				return nil, fmt.Errorf("include: ** must occupy a whole path segment")
			}
			if _, err := path.Match(part, ""); err != nil {
				return nil, fmt.Errorf("include: invalid glob: %w", err)
			}
			// Negated classes use ^ in Go path.Match and ripgrep's globset.
			if strings.Contains(part, "[!") {
				return nil, fmt.Errorf("include: use [^...] for a negated character class")
			}
		}
		if len(parts) == 1 {
			parts = append([]string{"**"}, parts...)
		}
		g.alternatives = append(g.alternatives, parts)
	}
	return g, nil
}

func (g *searchGlob) matches(root, file string) bool {
	if g == nil {
		return true
	}
	rel, err := filepath.Rel(root, file)
	if err != nil {
		return false
	}
	if rel == "." {
		rel = filepath.Base(file)
	} // explicitly named file
	name := strings.Split(filepath.ToSlash(rel), "/")
	for _, pattern := range g.alternatives {
		// Linear space dynamic programming prevents exponential ** backtracking.
		states := make([]bool, len(name)+1)
		states[0] = true
		for _, segment := range pattern {
			if segment == "**" {
				for j := 1; j < len(states); j++ {
					states[j] = states[j] || states[j-1]
				}
				continue
			}
			for j := len(name); j > 0; j-- {
				matched, _ := path.Match(segment, name[j-1])
				states[j] = states[j-1] && matched
			}
			states[0] = false
		}
		if states[len(name)] {
			return true
		}
	}
	return false
}

func searchFileIncluded(previews []*searchContext, root, file string) bool {
	return len(previews) == 0 || previews[0] == nil || previews[0].include.matches(root, file)
}

func searchLimitNotice(limit int) string {
	return fmt.Sprintf("[search limit reached: %d matches; results may be incomplete. Narrow query/path/include or raise max.]", limit)
}

func searchLimitedResult(text string, limit int, previews []*searchContext) Result {
	if len(previews) > 0 && previews[0] != nil {
		previews[0].limit = limit
	}
	return Result{Text: text + "\n" + searchLimitNotice(limit)}
}

// canDescend reports whether any descendant file could match. Unlike matches,
// it accepts a partial path and requires an unconsumed pattern segment for the
// eventual filename. ** states stay active while consuming directory segments.
func (g *searchGlob) canDescend(root, dir string) bool {
	if g == nil || dir == root {
		return true
	}
	// Basename globs and leading ** can match below any directory. Avoid path
	// splitting and automaton allocations for this common, unprunable case.
	for _, pattern := range g.alternatives {
		if pattern[0] == "**" {
			return true
		}
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return true
	} // prune only when we can prove impossibility
	name := strings.Split(filepath.ToSlash(rel), "/")
	for _, pattern := range g.alternatives {
		states := make([]bool, len(pattern)+1)
		next := make([]bool, len(states))
		states[0] = true
		for _, part := range name {
			// Epsilon closure: ** may consume zero segments.
			for i, segment := range pattern {
				if segment == "**" && states[i] {
					states[i+1] = true
				}
			}
			clear(next)
			for i, segment := range pattern {
				if !states[i] {
					continue
				}
				if segment == "**" {
					next[i] = true
					continue
				}
				matched, _ := path.Match(segment, part)
				if matched {
					next[i+1] = true
				}
			}
			states, next = next, states
		}
		// An active ** always permits another segment. Other active states have
		// at least one unconsumed segment, which could be the descendant filename.
		for _, possible := range states[:len(pattern)] {
			if possible {
				return true
			}
		}
	}
	return false
}
