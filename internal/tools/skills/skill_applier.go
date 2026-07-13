package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"supercli/internal/tools/core"
)

const (
	appliedSkillHeadBytes = 24 << 10
	appliedSkillTailBytes = 8 << 10
)

// SkillApplier searches and activates skills through the `apply_skill`
// meta-tool. Guidance is returned inside the tool result, making it an
// append-only part of conversation history that works in every front-end,
// survives resume, and does not rewrite the KV-cache prefix.
type SkillApplier struct {
	mu         sync.Mutex
	discoverer *Discoverer
	applied    map[string]struct{} // dedupe
	order      []string
	content    map[string]string // capped guidance, cached after one lazy load
	details    map[string]appliedDetail
	preamble   string // separator inserted between skills
}

type appliedDetail struct {
	path        string
	risk        string
	sourceBytes int
}

// NewSkillApplier builds a SkillApplier. Loaded bodies are cached so a shared
// coordinator/worker registry never decompresses the same skill twice.
func NewSkillApplier(d *Discoverer) *SkillApplier {
	return &SkillApplier{
		discoverer: d,
		applied:    make(map[string]struct{}),
		content:    make(map[string]string),
		details:    make(map[string]appliedDetail),
		preamble:   "\n\n",
	}
}

// Spec returns the meta-tool description. Always-on: the
// model can call it in any turn.
func (s *SkillApplier) Spec() Tool {
	return Tool{
		Name: "apply_skill",
		Description: "Search or activate an installed skill. Pass query to find " +
			"matching skills without loading their bodies; then pass name to apply " +
			"one. Applied guidance is returned once as an append-only tool result " +
			"and remains in conversation context.",
		Schema: `{
			"type": "object",
			"properties": {
				"name": {"type": "string", "description": "exact skill name to apply"},
				"query": {"type": "string", "description": "keywords to search when the name is unknown"},
				"limit": {"type": "integer", "default": 5, "maximum": 10}
			}
		}`,
		Fn: s.execute,
	}
}

// applyArgs is the JSON shape the model sends.
type applyArgs struct {
	Name  string `json:"name"`
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// execute either searches metadata or resolves one skill and returns its
// bounded guidance. Only this selected body is read and materialized.
func (s *SkillApplier) execute(_ context.Context, args json.RawMessage) (Result, error) {
	var a applyArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("apply_skill: bad args: %w", err)}, nil
	}
	name := strings.TrimSpace(a.Name)
	if name == "" {
		query := strings.TrimSpace(a.Query)
		if query == "" {
			return Result{Err: fmt.Errorf("apply_skill: provide name to apply or query to search")}, nil
		}
		return s.search(query, a.Limit)
	}
	s.mu.Lock()
	if guidance, ok := s.content[name]; ok {
		detail := s.details[name]
		result := appliedSkillResult(name, detail.path, detail.risk, guidance, detail.sourceBytes)
		s.mu.Unlock()
		return Result{Text: result}, nil
	}
	s.mu.Unlock()

	skill, err := s.discoverer.Get(name)
	if err != nil {
		return Result{Err: fmt.Errorf("apply_skill: %w", err)}, nil
	}
	guidance := core.HeadTail(skill.Content, appliedSkillHeadBytes, appliedSkillTailBytes)
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.content[skill.Name]; ok {
		// One SkillApplier may be shared by coordinator and worker registries.
		// Return the cached guidance again so a different conversation receives
		// it too; no archive scan or decompression is repeated.
		return Result{Text: appliedSkillResult(skill.Name, skill.Path, skill.Risk, cached, len(skill.Content))}, nil
	}
	s.applied[skill.Name] = struct{}{}
	s.order = append(s.order, skill.Name)
	s.content[skill.Name] = guidance
	s.details[skill.Name] = appliedDetail{path: skill.Path, risk: skill.Risk, sourceBytes: len(skill.Content)}
	return Result{Text: appliedSkillResult(skill.Name, skill.Path, skill.Risk, guidance, len(skill.Content))}, nil
}

func appliedSkillResult(name, path, risk, guidance string, sourceBytes int) string {
	return fmt.Sprintf(
		"skill_applied name=%q guidance_bytes=%d source_bytes=%d risk=%s\nresource_path: %s\n<skill-guidance>\n%s\n</skill-guidance>",
		name, len(guidance), sourceBytes, emptyAs(risk, "unknown"), path, guidance,
	)
}

func (s *SkillApplier) search(query string, limit int) (Result, error) {
	hits, err := s.discoverer.Search(query, limit)
	if err != nil {
		return Result{Err: fmt.Errorf("apply_skill: search: %w", err)}, nil
	}
	type match struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Category    string `json:"category,omitempty"`
		Risk        string `json:"risk,omitempty"`
	}
	resp := struct {
		Query   string  `json:"query"`
		Matches []match `json:"matches"`
		Hint    string  `json:"hint"`
	}{Query: query, Hint: "Call apply_skill again with the exact name of one matching skill."}
	for _, hit := range hits {
		resp.Matches = append(resp.Matches, match{
			Name: hit.Name, Description: compactDescription(hit.Description, 220),
			Category: hit.Category, Risk: hit.Risk,
		})
	}
	if len(resp.Matches) == 0 {
		resp.Hint = "No skill matched. Try shorter or English keywords."
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return Result{Err: fmt.Errorf("apply_skill: search response: %w", err)}, nil
	}
	return Result{Text: string(b)}, nil
}

func compactDescription(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func emptyAs(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// AppendSkills returns cached guidance in activation order for diagnostics
// and embedders. The normal agent path consumes guidance directly from each
// append-only apply_skill result.
func (s *SkillApplier) AppendSkills() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.order) == 0 {
		return ""
	}
	var b strings.Builder
	for _, n := range s.order {
		b.WriteString(s.preamble)
		b.WriteString("## Skill: ")
		b.WriteString(n)
		b.WriteString("\n\n")
		b.WriteString(s.content[n])
	}
	return b.String()
}

// Applied returns the names of applied skills, in order.
// Useful for tests and the /skills TUI command.
func (s *SkillApplier) Applied() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.order))
	copy(out, s.order)
	return out
}

// Reset clears the cached activation set for callers that reuse an applier
// across otherwise independent sessions.
func (s *SkillApplier) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applied = make(map[string]struct{})
	s.order = nil
	s.content = make(map[string]string)
	s.details = make(map[string]appliedDetail)
}
