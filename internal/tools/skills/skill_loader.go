package skills

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill is a discovered SKILL.md file. Content is the full
// markdown body (frontmatter stripped). Frontmatter holds
// optional metadata. The Discoverer assigns Priority based on
// the directory the skill came from (project > user > builtin).
type Skill struct {
	Name        string
	Path        string
	Description string
	Tags        []string
	Priority    int
	Content     string
	Frontmatter map[string]string
}

// Source is one search root for skill discovery, tagged with
// a priority (higher = wins on conflict).
type Source struct {
	Dir      string
	Priority int
}

// Discoverer scans a set of directories for SKILL.md files.
// It applies a stable priority order: project > user >
// builtin. When two skills share a name, the higher-priority
// source wins.
type Discoverer struct {
	Sources []Source
}

// NewDiscoverer builds a Discoverer with the standard source
// order. Callers may append additional sources; priority is
// bumped above the builtin baseline.
// dataDir is the resolved SuperCli data directory (portable:
// supercli-data next to the executable) holding the cross-project
// skills. ~/.claude/skills is still scanned read-only for
// interoperability with Claude Code skills.
func NewDiscoverer(projectDir, dataDir string) *Discoverer {
	sources := []Source{
		// Project-level: highest priority.
		{Dir: filepath.Join(projectDir, "skills"), Priority: 100},
		{Dir: filepath.Join(projectDir, ".supercli", "skills"), Priority: 95},
	}
	// Read-only interop: Claude Code user skills, if present.
	if uh, err := os.UserHomeDir(); err == nil && uh != "" {
		sources = append(sources, Source{Dir: filepath.Join(uh, ".claude", "skills"), Priority: 50})
	}
	sources = append(sources,
		// User-level (cross-project), inside the portable data dir.
		Source{Dir: filepath.Join(dataDir, "skills"), Priority: 45},
		// Builtin: lowest priority.
		Source{Dir: filepath.Join(dataDir, "skills", "builtin"), Priority: 10},
	)
	return &Discoverer{Sources: sources}
}

// Discover walks all sources and returns the merged skill set,
// sorted by (Priority desc, Name asc). Missing directories
// are silently skipped.
func (d *Discoverer) Discover() ([]Skill, error) {
	byName := make(map[string]Skill)
	order := []string{} // preserve discovery order for stable ties
	for _, src := range d.Sources {
		if src.Dir == "" {
			continue
		}
		entries, err := os.ReadDir(src.Dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %q: %w", src.Dir, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			// Each subdir is one skill; SKILL.md is the
			// entry point.
			skillPath := filepath.Join(src.Dir, e.Name(), "SKILL.md")
			s, err := readSkill(skillPath)
			if err != nil {
				// Missing SKILL.md is fine — silently
				// skip the dir. Other errors surface.
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("read %q: %w", skillPath, err)
			}
			if s.Name == "" {
				s.Name = e.Name()
			}
			s.Priority = src.Priority
			// On duplicate, keep the higher-priority
			// one (earlier source = higher priority
			// in our convention).
			existing, ok := byName[s.Name]
			if !ok || src.Priority > existing.Priority {
				if !ok {
					order = append(order, s.Name)
				}
				byName[s.Name] = s
			}
		}
	}
	out := make([]Skill, 0, len(order))
	for _, n := range order {
		out = append(out, byName[n])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Get returns a single skill by name from the discovery set.
// Returns an error if the skill is not found.
func (d *Discoverer) Get(name string) (*Skill, error) {
	all, err := d.Discover()
	if err != nil {
		return nil, err
	}
	for _, s := range all {
		if s.Name == name {
			return &s, nil
		}
	}
	return nil, fmt.Errorf("skill %q not found", name)
}

// readSkill reads a SKILL.md file, parses optional YAML
// frontmatter (delimited by ---), and returns the Skill. The
// frontmatter is intentionally parsed by hand to avoid a YAML
// dependency: we only support `key: value` lines. Comments
// and nested structures are not allowed in F4.
func readSkill(path string) (Skill, error) {
	f, err := os.Open(path)
	if err != nil {
		return Skill{}, err
	}
	defer f.Close()
	var (
		content     strings.Builder
		fm          = make(map[string]string)
		inFront     bool
		frontClosed bool
	)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !frontClosed && strings.TrimSpace(line) == "---" {
			if !inFront {
				inFront = true
				continue
			}
			frontClosed = true
			continue
		}
		if inFront && !frontClosed {
			// Parse "key: value" frontmatter lines.
			idx := strings.Index(line, ":")
			if idx > 0 {
				k := strings.TrimSpace(line[:idx])
				v := strings.TrimSpace(line[idx+1:])
				fm[k] = v
			}
			continue
		}
		content.WriteString(line)
		content.WriteString("\n")
	}
	if err := sc.Err(); err != nil {
		return Skill{}, err
	}
	s := Skill{
		Path:        path,
		Content:     strings.TrimSpace(content.String()),
		Frontmatter: fm,
	}
	// Pull common frontmatter fields into typed fields.
	s.Name = fm["name"]
	s.Description = fm["description"]
	if tags := fm["tags"]; tags != "" {
		// Allow comma- or bracket-style tags.
		tags = strings.Trim(tags, "[]")
		for _, t := range strings.Split(tags, ",") {
			t = strings.TrimSpace(strings.Trim(t, "\""))
			if t != "" {
				s.Tags = append(s.Tags, t)
			}
		}
	}
	if s.Description == "" {
		// Fall back: first non-empty content line.
		for _, l := range strings.Split(s.Content, "\n") {
			l = strings.TrimSpace(l)
			if l != "" && !strings.HasPrefix(l, "#") {
				s.Description = l
				break
			}
		}
	}
	return s, nil
}
