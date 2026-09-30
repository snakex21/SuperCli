package skills

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
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
	Category    string
	Risk        string
	Source      string
	Priority    int
	Content     string
	Frontmatter map[string]string
	searchName  string
	searchText  string
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
	Sources  []Source
	Builtin  *BuiltinPack
	once     sync.Once
	cached   []Skill
	cacheErr error
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

// NewDiscovererWithBuiltins adds the complete embedded skill catalog. The
// regular constructor remains useful to embedders and tests that want only
// filesystem sources.
func NewDiscovererWithBuiltins(projectDir, dataDir string) *Discoverer {
	d := NewDiscoverer(projectDir, dataDir)
	d.Builtin = NewBuiltinPack(dataDir)
	return d
}

// Discover returns a snapshot with external skill bodies for legacy callers.
// Search and List use only metadata; Get loads one selected body.
// Results are sorted by (Priority desc, Name asc). Directories are scanned once;
// restart SuperCli after installing a new external skill.
func (d *Discoverer) Discover() ([]Skill, error) {
	all, err := d.catalog()
	if err != nil {
		return nil, err
	}
	out := make([]Skill, len(all))
	for i, metadata := range all {
		out[i] = metadata
		if !strings.HasPrefix(metadata.Path, builtinScheme) {
			selected, err := d.loadExternal(metadata)
			if err != nil {
				return nil, err
			}
			out[i] = *selected
		}
	}
	return out, nil
}

func (d *Discoverer) catalog() ([]Skill, error) {
	d.once.Do(func() {
		d.cached, d.cacheErr = d.discoverUncached()
	})
	return d.cached, d.cacheErr
}

func (d *Discoverer) discoverUncached() ([]Skill, error) {
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
			s, err := readSkillMetadata(skillPath)
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
			s.Frontmatter = nil
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
	if d.Builtin != nil {
		builtin, err := d.Builtin.Catalog()
		if err != nil {
			return nil, err
		}
		for _, s := range builtin {
			if _, exists := byName[s.Name]; exists {
				continue
			}
			byName[s.Name] = s
			order = append(order, s.Name)
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
	for i := range out {
		out[i].searchName = strings.ToLower(out[i].Name)
		out[i].searchText = strings.ToLower(out[i].Name + " " + out[i].Description + " " + out[i].Category + " " + strings.Join(out[i].Tags, " "))
	}
	return out, nil
}

// Get returns a single skill by name from the discovery set.
// Returns an error if the skill is not found.
func (d *Discoverer) Get(name string) (*Skill, error) {
	all, err := d.catalog()
	if err != nil {
		return nil, err
	}
	for _, s := range all {
		if s.Name == name {
			if strings.HasPrefix(s.Path, builtinScheme) && d.Builtin != nil {
				return d.Builtin.Load(s.Name)
			}
			return d.loadExternal(s)
		}
	}
	return nil, fmt.Errorf("skill %q not found", name)
}

func (d *Discoverer) loadExternal(metadata Skill) (*Skill, error) {
	selected, err := readSkill(metadata.Path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", metadata.Path, err)
	}
	selected.Name, selected.Priority = metadata.Name, metadata.Priority
	return &selected, nil
}

// Search ranks skill metadata without opening any SKILL.md bodies. It is a
// deliberately small lexical matcher: 1410 entries fit comfortably in memory
// and a linear pass is faster and cheaper than another SQLite/FTS handle.
func (d *Discoverer) Search(query string, limit int) ([]Skill, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("skill search query is empty")
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}
	all, err := d.catalog()
	if err != nil {
		return nil, err
	}
	qt := skillTokens(query)
	type scored struct {
		skill Skill
		score int
	}
	ranked := make([]scored, 0, limit)
	qLower := strings.ToLower(query)
	for _, s := range all {
		score := skillMatchScore(s, qLower, qt)
		if score > 0 {
			s.Content = ""
			candidate := scored{skill: s, score: score}
			pos := len(ranked)
			for i := range ranked {
				if candidate.score > ranked[i].score ||
					(candidate.score == ranked[i].score && candidate.skill.Name < ranked[i].skill.Name) {
					pos = i
					break
				}
			}
			if pos >= limit {
				continue
			}
			if len(ranked) < limit {
				ranked = append(ranked, scored{})
			}
			copy(ranked[pos+1:], ranked[pos:len(ranked)-1])
			ranked[pos] = candidate
		}
	}
	out := make([]Skill, len(ranked))
	for i := range ranked {
		out[i] = ranked[i].skill
	}
	return out, nil
}

// List returns one metadata-only catalog page and the total number of matches.
// It reads the shared immutable catalog directly, avoiding the full slice copy
// made by Discover when a UI only needs a small visible window.
func (d *Discoverer) List(query string, offset, limit int) ([]Skill, int, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 50
	}
	all, err := d.catalog()
	if err != nil {
		return nil, 0, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	out := make([]Skill, 0, min(limit, len(all)))
	total := 0
	for _, skill := range all {
		if query != "" && !strings.Contains(skill.searchText, query) {
			continue
		}
		if total >= offset && len(out) < limit {
			skill.Content = ""
			skill.Frontmatter = nil
			out = append(out, skill)
		}
		total++
	}
	return out, total, nil
}

func skillMatchScore(skill Skill, query string, tokens []string) int {
	score := 0
	switch {
	case skill.searchName == query:
		score += 100
	case strings.HasPrefix(skill.searchName, query):
		score += 40
	case strings.Contains(skill.searchName, query):
		score += 25
	}
	for _, token := range tokens {
		if strings.Contains(skill.searchName, token) {
			score += 12
		} else if strings.Contains(skill.searchText, token) {
			score += 3
		}
	}
	return score
}

// Automatic selection is deliberately conservative: exact unique names, or
// multiple name tokens with broad query coverage and a clear runner-up gap.
func clearSkillMatch(query string, hits []Skill) bool {
	if len(hits) == 0 {
		return false
	}
	query = strings.ToLower(strings.TrimSpace(query))
	best := hits[0]
	if best.searchName == query {
		return len(hits) == 1 || hits[1].searchName != query
	}
	tokens := skillTokens(query)
	nameMatches, matches := 0, 0
	for _, token := range tokens {
		if strings.Contains(best.searchText, token) {
			matches++
		}
		if strings.Contains(best.searchName, token) {
			nameMatches++
		}
	}
	if nameMatches < 2 || matches*4 < len(tokens)*3 {
		return false
	}
	return len(hits) == 1 || skillMatchScore(best, query, tokens)-skillMatchScore(hits[1], query, tokens) >= 12
}

func skillTokens(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	out := fields[:0]
	seen := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		switch f {
		case "the", "for", "of", "with", "to", "and", "or", "in", "on", "our", "this", "that":
			continue
		}
		if len([]rune(f)) < 2 {
			continue
		}
		if _, exists := seen[f]; exists {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out
}

const skillMetadataReadLimit = 32 << 10

func readSkill(path string) (Skill, error)         { return readSkillFile(path, true) }
func readSkillMetadata(path string) (Skill, error) { return readSkillFile(path, false) }

// Metadata scanning stops at the header or first descriptive paragraph. Its
// bounded reader prevents an unrelated large body from slowing or breaking the
// catalog. The selected skill is read separately, without Scanner's line limit.
func readSkillFile(path string, body bool) (Skill, error) {
	f, err := os.Open(path)
	if err != nil {
		return Skill{}, err
	}
	defer f.Close()
	var input io.Reader = f
	if !body {
		input = io.LimitReader(f, skillMetadataReadLimit)
	}
	reader := bufio.NewReader(input)
	fm := make(map[string]string)
	var content strings.Builder
	first, inFront := true, false
	fallback := ""
	blockKey, blockSeparator := "", ""
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return Skill{}, readErr
		}
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		headerLine := false
		if first && trimmed != "" {
			first = false
			if trimmed == "---" {
				inFront, headerLine = true, true
			}
		} else if inFront {
			headerLine = true
			if trimmed == "---" {
				inFront = false
				if !body && fm["description"] != "" {
					break
				}
			} else if blockKey != "" && (trimmed == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
				if trimmed != "" {
					if fm[blockKey] != "" {
						fm[blockKey] += blockSeparator
					}
					fm[blockKey] += trimmed
				}
			} else {
				blockKey = ""
				if idx := strings.Index(line, ":"); idx > 0 {
					key, value := strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:])
					if value == ">" || value == ">-" || value == "|" || value == "|-" {
						blockKey, blockSeparator = key, " "
						if strings.HasPrefix(value, "|") {
							blockSeparator = "\n"
						}
						fm[key] = ""
					} else {
						fm[key] = skillScalar(value)
					}
				}
			}
		}
		if !headerLine && !inFront {
			if body {
				content.WriteString(line)
			}
			if fallback == "" && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				fallback = trimmed
				if !body {
					break
				}
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	s := Skill{Path: path, Content: strings.TrimSpace(content.String()), Frontmatter: fm,
		Name: fm["name"], Description: fm["description"], Category: fm["category"], Risk: fm["risk"], Source: fm["source"]}
	if s.Description == "" {
		s.Description = fallback
	}
	if tags := fm["tags"]; tags != "" {
		for _, tag := range strings.Split(strings.Trim(tags, "[]"), ",") {
			if tag = skillScalar(strings.TrimSpace(tag)); tag != "" {
				s.Tags = append(s.Tags, tag)
			}
		}
	}
	return s, nil
}

func skillScalar(value string) string {
	if len(value) < 2 {
		return value
	}
	if value[0] == '"' && value[len(value)-1] == '"' {
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
	}
	if value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}
	return value
}
